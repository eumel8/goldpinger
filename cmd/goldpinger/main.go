// Copyright 2018 Bloomberg Finance L.P.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/go-openapi/loads"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/utils/net"

	"github.com/bloomberg/goldpinger/v3/pkg/goldpinger"
	"github.com/bloomberg/goldpinger/v3/pkg/restapi"
	"github.com/bloomberg/goldpinger/v3/pkg/restapi/operations"
	flags "github.com/jessevdk/go-flags"
)

// these will be injected during build in build.sh script to allow printing
var (
	Version, Build string
)

func getLogger(logLevel string) (*zap.Logger, error) {
	var cfg zap.Config

	// Set base config based on log level
	switch strings.ToLower(logLevel) {
	case "debug":
		cfg = zap.NewDevelopmentConfig()
		cfg.Level = zap.NewAtomicLevelAt(zap.DebugLevel)
	case "info":
		cfg = zap.NewProductionConfig()
		cfg.Level = zap.NewAtomicLevelAt(zap.InfoLevel)
	case "warn":
		cfg = zap.NewProductionConfig()
		cfg.Level = zap.NewAtomicLevelAt(zap.WarnLevel)
	case "error":
		cfg = zap.NewProductionConfig()
		cfg.Level = zap.NewAtomicLevelAt(zap.ErrorLevel)
	default:
		cfg = zap.NewProductionConfig()
		cfg.Level = zap.NewAtomicLevelAt(zap.InfoLevel)
	}

	// Use console encoding for better readability
	cfg.Encoding = "console"
	cfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder

	logger, err := cfg.Build()
	if err != nil {
		return nil, fmt.Errorf("Could not build logger: %w", err)
	}

	return logger, nil
}

func main() {
	// load embedded swagger file
	swaggerSpec, err := loads.Analyzed(restapi.SwaggerJSON, "")
	if err != nil {
		log.Fatalf("Could not parse swagger: %v", err)
	}

	// create new service API
	api := operations.NewGoldpingerAPI(swaggerSpec)
	server := restapi.NewServer(api)
	defer server.Shutdown()

	parser := flags.NewParser(server, flags.Default)
	parser.ShortDescription = "Goldpinger"
	parser.LongDescription = swaggerSpec.Spec().Info.Description

	// parse flags
	server.ConfigureFlags()
	for _, optsGroup := range api.CommandLineOptionsGroups {
		_, err := parser.AddGroup(optsGroup.ShortDescription, optsGroup.LongDescription, optsGroup.Options)
		if err != nil {
			log.Fatalf("Could not add flag group: %v", err)
		}
	}

	if _, err := parser.Parse(); err != nil {
		code := 1
		if fe, ok := err.(*flags.Error); ok {
			if fe.Type == flags.ErrHelp {
				code = 0
			}
		}
		os.Exit(code)
	}

	// Configure logger
	logger, err := getLogger(goldpinger.GoldpingerConfig.LogLevel)
	if err != nil {
		log.Fatalf("Could not build logger: %v", err)
	}
	defer logger.Sync()

	// Set as global logger
	zap.ReplaceGlobals(logger)

	undo := zap.RedirectStdLog(logger)
	defer undo()

	logger.Info("Goldpinger", zap.String("version", Version), zap.String("build", Build))

	if goldpinger.GoldpingerConfig.Namespace == nil {
		goldpinger.GoldpingerConfig.Namespace = &goldpinger.PodNamespace
	} else {
		logger.Info("Using configured namespace", zap.String("namespace", *goldpinger.GoldpingerConfig.Namespace))
	}

	// make a kubernetes client
	var config *rest.Config
	if goldpinger.GoldpingerConfig.KubeConfigPath == "" {
		logger.Info("Kubeconfig not specified, trying to use in cluster config")
		config, err = rest.InClusterConfig()
	} else {
		logger.Info("Kubeconfig specified", zap.String("path", goldpinger.GoldpingerConfig.KubeConfigPath))
		config, err = clientcmd.BuildConfigFromFlags("", goldpinger.GoldpingerConfig.KubeConfigPath)
	}
	if err != nil {
		logger.Fatal("Error getting config ", zap.Error(err))
	}
	// communicate to kube-apiserver with protobuf
	config.AcceptContentTypes = strings.Join([]string{runtime.ContentTypeProtobuf, runtime.ContentTypeJSON}, ",")
	config.ContentType = runtime.ContentTypeProtobuf

	// create the clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		logger.Fatal("kubernetes.NewForConfig error ", zap.Error(err))
	}
	goldpinger.GoldpingerConfig.KubernetesClient = clientset

	// Check if we have an override for the client, default to own port
	if goldpinger.GoldpingerConfig.Port == 0 {
		goldpinger.GoldpingerConfig.Port = server.Port
	}

	if goldpinger.GoldpingerConfig.PodIP == "" {
		logger.Info("PodIP not set: pinging all pods")
	}
	if goldpinger.GoldpingerConfig.PingNumber == 0 {
		logger.Info("--ping-number set to 0: pinging all pods")
	}
	if goldpinger.GoldpingerConfig.IPVersions == nil || len(goldpinger.GoldpingerConfig.IPVersions) == 0 {
		logger.Info("IPVersions not set: settings to 4 (IPv4)")
		goldpinger.GoldpingerConfig.IPVersions = []string{"4"}
	}
	if len(goldpinger.GoldpingerConfig.IPVersions) > 1 {
		logger.Warn("Multiple IP versions not supported. Will use first version specified as default", zap.Strings("IPVersions", goldpinger.GoldpingerConfig.IPVersions))
	}
	if goldpinger.GoldpingerConfig.IPVersions[0] != string(net.IPv4) && goldpinger.GoldpingerConfig.IPVersions[0] != string(net.IPv6) {
		logger.Error("Unknown IP version specified: expected values are 4 or 6", zap.Strings("IPVersions", goldpinger.GoldpingerConfig.IPVersions))
	}

	// Handle deprecated flags
	if int(goldpinger.GoldpingerConfig.PingTimeout) == 0 {
		logger.Warn("ping-timeout-ms is deprecated in favor of ping-timeout and will be removed in the future",
			zap.Int64("ping-timeout-ms", goldpinger.GoldpingerConfig.PingTimeoutMs))
		goldpinger.GoldpingerConfig.PingTimeout = time.Duration(goldpinger.GoldpingerConfig.PingTimeoutMs) * time.Millisecond
	}
	if int(goldpinger.GoldpingerConfig.CheckTimeout) == 0 {
		logger.Warn("check-timeout-ms is deprecated in favor of check-timeout and will be removed in the future",
			zap.Int64("check-timeout-ms", goldpinger.GoldpingerConfig.CheckTimeoutMs))
		goldpinger.GoldpingerConfig.CheckTimeout = time.Duration(goldpinger.GoldpingerConfig.CheckTimeoutMs) * time.Millisecond
	}
	if int(goldpinger.GoldpingerConfig.CheckAllTimeout) == 0 {
		logger.Warn("check-all-timeout-ms is deprecated in favor of check-all-timeout will be removed in the future",
			zap.Int64("check-all-timeout-ms", goldpinger.GoldpingerConfig.CheckAllTimeoutMs))
		goldpinger.GoldpingerConfig.CheckAllTimeout = time.Duration(goldpinger.GoldpingerConfig.CheckAllTimeoutMs) * time.Millisecond
	}

	server.ConfigureAPI()
	goldpinger.StartUpdater()

	logger.Info("All good, starting serving the API")

	// serve API
	if err := server.Serve(); err != nil {
		logger.Fatal("Error serving the API", zap.Error(err))
	}
}
