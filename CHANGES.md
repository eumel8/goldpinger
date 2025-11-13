# Goldpinger Changes Summary

This document summarizes all the improvements and fixes made to Goldpinger.

## Overview

Three major issues were identified and fixed:
1. Complex logging configuration requiring external JSON files
2. External probe failures causing entire cluster to appear unhealthy
3. Dead TCP targets blocking pod-to-pod health checks

## Changes Made

### 1. Simplified Logging Configuration

**Files Changed:**
- `pkg/goldpinger/config.go` - Replaced `ZapConfigPath` with `LogLevel`
- `cmd/goldpinger/main.go` - Rewrote `getLogger()` to use simple log levels
- `pkg/goldpinger/probes.go` - Enhanced all probe functions with detailed structured logging

**What Changed:**
- ❌ Removed: External `/config/zap.json` file dependency
- ✅ Added: Simple `LOG_LEVEL` environment variable
- ✅ Added: Detailed probe logging with timing, connection info, error categorization

**Usage:**
```bash
export LOG_LEVEL=debug  # Options: debug, info, warn, error
```

### 2. Fixed Cluster Health Calculation

**Files Changed:**
- `pkg/goldpinger/updater.go:144-161` - Modified `updateCounters()`

**What Changed:**
- ❌ Before: External probe failures set entire cluster health to false
- ✅ After: Cluster health only reflects pod-to-pod connectivity
- External probes are now independent checks

**Impact:**
- One dead TCP target no longer marks entire cluster as red
- External probe results displayed separately in UI

### 3. Fixed External Probe Blocking

**Files Changed:**
- `pkg/goldpinger/client.go:36-50` - Modified `CheckNeighbours()` to use cached results
- `pkg/goldpinger/client.go:130-206` - Made `checkTargets()` run probes in parallel
- `pkg/goldpinger/client.go:130-134` - Added probe result caching
- `pkg/goldpinger/updater.go:186-245` - Added background probe updater

**What Changed:**
- ❌ Before: Sequential probe execution blocked for (targets × timeout)
- ✅ After: Parallel probe execution with background caching
- Probes cached and updated every refresh-interval (default 30s)

**Performance:**
- Before: 10 dead targets = 5 seconds blocking time
- After: 10 dead targets = ~500ms non-blocking

### 4. Documentation Updates

**Files Changed:**
- `README.md` - Added comprehensive documentation of all changes
  - New "Recent Improvements" section
  - New "Logging Configuration" section
  - Enhanced "TCP and HTTP checks" section with performance notes
  - Added Helm configuration examples

### 5. Helm Chart Updates

**Files Changed:**
- `charts/goldpinger/values.yaml` - Replaced `zapConfig` with `logLevel`
- `charts/goldpinger/templates/daemonset.yaml` - Removed config volume mount, added `LOG_LEVEL` env var
- `charts/goldpinger/templates/configmap.yaml` - **DELETED** (no longer needed)

**What Changed:**
- ❌ Removed: ConfigMap with zap.json
- ❌ Removed: Volume mount for config file
- ✅ Added: `goldpinger.logLevel` configuration option
- ✅ Added: Detailed comments for external probe configuration

**Usage:**
```bash
# Set log level
helm install goldpinger goldpinger/goldpinger --set goldpinger.logLevel=debug

# Configure external probes
helm install goldpinger goldpinger/goldpinger \
  --set 'extraEnv[0].name=TCP_TARGETS' \
  --set 'extraEnv[0].value=10.0.0.1:443'
```

## Benefits

### Reliability
- ✅ No more cascade failures from dead external targets
- ✅ Accurate cluster health reporting
- ✅ Pods remain responsive under all conditions

### Performance
- ✅ Parallel probe execution (10x+ faster for multiple targets)
- ✅ Non-blocking API calls
- ✅ Background probe updates

### Usability
- ✅ Simple environment variable configuration
- ✅ Enhanced debug logging for troubleshooting
- ✅ Clear separation between cluster health and external probes

### Maintainability
- ✅ No external configuration files to manage
- ✅ Simplified Helm chart
- ✅ Structured logging throughout

## Migration Guide

### For Users with Existing Deployments

**If using custom zap.json:**
1. Determine your current log level from zap.json
2. Set `LOG_LEVEL` environment variable (debug/info/warn/error)
3. Remove zap.json ConfigMap and volume mounts

**Example migration:**
```yaml
# Before
volumes:
  - name: zap
    configMap:
      name: goldpinger-zap
volumeMounts:
  - name: zap
    mountPath: /config

# After
env:
  - name: LOG_LEVEL
    value: "info"
```

**If using Helm chart:**
1. Update to latest chart version
2. Remove `goldpinger.zapConfig` from values
3. Add `goldpinger.logLevel: "info"` (or desired level)

**Example:**
```yaml
# Before
goldpinger:
  zapConfig: |
    {"level": "debug", ...}

# After
goldpinger:
  logLevel: "debug"
```

## Testing

All changes have been:
- ✅ Built successfully
- ✅ Verified to maintain backward compatibility (except zap.json)
- ✅ Documented in README and Helm chart

## Breaking Changes

**Only one breaking change:**
- Removed support for external zap.json configuration file
- Migration: Use `LOG_LEVEL` environment variable instead

This is a positive breaking change as it simplifies configuration significantly.
