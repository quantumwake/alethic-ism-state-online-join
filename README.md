# Alethic ISM State Online Join Processor

## Overview

The Alethic ISM State Online Join Processor is a Go-based microservice that performs real-time data correlation and joining operations within the Alethic Instruction-Based State Machine (ISM) ecosystem. It implements a sophisticated sliding window cache mechanism to join events from multiple sources based on configurable key definitions.

## Core Functionality

### Primary Purpose
This service functions as a streaming data processor that:
- Joins two or more state entries together to form a single state entry
- Uses windowing online heap processing for efficient memory management
- Applies configurable state join definitions
- Correlates incoming events based on defined key fields

### Key Features
- **Real-time Processing**: Processes events as they arrive with minimal latency
- **Per-Event TTL Eviction**: Individual event time-to-live management for precise cache control
- **Sliding Window Cache**: Maintains temporal context for joining events with configurable windows
- **Cross-source Joining**: Prevents self-joins by only correlating events from different sources
- **Configurable Join Keys**: Flexible key definitions for different correlation scenarios
- **Performance Monitoring**: Built-in statistics tracking and performance metrics
- **Dual Eviction Policies**: Soft and hard eviction windows for optimal memory management

## Architecture

### System Components

#### 1. Message Processing Layer
- **NATS Integration**: Uses NATS for message queuing and routing
- **Route-based Processing**: Configurable message routes defined in `routing.yaml`
- **Multi-processor Support**: Handles various processor types:
  - Language model processors
  - Code executors
  - Data transformers
  - State synchronizers

#### 2. Join Processing Engine (`pkg/correlate/block.go`)
The core joining logic implements:
- **Per-Event TTL Management**: Each event maintains its own time-to-live for granular eviction control
- **Sliding Window Cache** with enhanced eviction policies:
  - Per-event TTL: Individual event lifecycle management
  - Soft window (configurable, default 1 minute): Active joining period
  - Hard window (configurable, default 1 minute): Maximum data retention and cache flush period
- **Key-based Correlation**: Extracts and matches events using configured key fields
- **Efficient Memory Management**: Automatic cache eviction based on per-event TTL and capacity
- **Block Partitioning**: New `blockpart.go` for improved cache block organization

#### 3. Data Persistence Layer
- **PostgreSQL Backend**: Uses GORM ORM for database operations
- **State Management**: Persists route configurations and join definitions
- **Transaction Support**: Ensures data consistency during processing

### Data Flow

```
1. Input Stage
   └─> RouteMessage arrives via NATS
       └─> Contains query state data with source identifier

2. Processing Stage
   └─> Extract correlation keys using join key definitions
       └─> Find or create cache block for the key
           └─> Check for matching events from different sources
               └─> Perform join operation if matches found

3. Output Stage
   └─> Publish joined results to state sync routes
       └─> Update performance statistics
```

## Join Algorithm

The join algorithm operates as follows:

1. **Event Reception**: Incoming event arrives with source identifier
2. **Key Extraction**: Extract correlation key using configured key definitions
3. **Cache Lookup**: Find or create cache block for the extracted key
4. **Join Processing**: For each existing event in block from different sources:
   - Perform join operation
   - Preserve key fields as-is
   - Combine non-key fields
   - Add timestamp metadata
5. **Cache Storage**: Store new event in cache block
6. **Timer Reset**: Reset eviction timers for the block

### Cache Eviction Strategy

The service now implements a sophisticated per-event TTL eviction mechanism:

#### Per-Event TTL
- **Individual Event Lifecycle**: Each event tracks its own creation time and TTL
- **Granular Control**: Events can be evicted independently based on their specific TTL
- **Memory Optimization**: Prevents stale events from consuming memory unnecessarily

#### Eviction Policies
- **Per-Event Eviction**: Events are automatically removed when their individual TTL expires
- **Soft Eviction**: When total blocks exceed threshold (configurable, default 10), remove blocks with expired events
- **Hard Eviction**: Force removal of any blocks older than hard window (configurable, default 1 minute)
- **Automatic Cleanup**: Background process continuously monitors and evicts expired events
- **Performance Impact**: Improved memory efficiency through granular eviction control

## Configuration

### Environment Variables
The service uses environment-based configuration for:
- Database connection settings
- NATS messaging configuration
- Service endpoint definitions
- Logging and monitoring settings

### Route Configuration (`routing.yaml`)
Defines message routing rules including:
- Input and output topics
- Processor assignments
- Join key definitions
- Error handling policies

### Join Key Definitions
Configurable per state output, allowing flexible correlation strategies:
```yaml
join_keys:
  - field: "user_id"
    ttl: 30s  # Optional: per-key TTL override
  - field: "session_id"
    ttl: 60s  # Optional: per-key TTL override
  - field: "correlation_id"
    ttl: 120s # Optional: per-key TTL override
```

### Event TTL Configuration
Configure time-to-live for events at multiple levels:
```yaml
# Global default TTL
default_event_ttl: 60s

# Per-route TTL override
routes:
  - name: "high_priority_join"
    event_ttl: 30s  # Shorter TTL for high-priority events
  - name: "batch_processing"
    event_ttl: 300s # Longer TTL for batch operations

# Per-event TTL (set in event metadata)
event:
  metadata:
    ttl: "45s"  # Event-specific TTL override
```

## Deployment

### Container Support
- **Docker**: Full Docker support with multi-stage builds
- **Base Image**: Uses `golang:1.24.1-alpine` for minimal footprint
- **Production Image**: Distroless container for security

### Kubernetes Integration
- Deployment manifests included
- ConfigMap and Secret management
- Service discovery configuration
- Health check endpoints

### Infrastructure Requirements
- PostgreSQL database (version 12+)
- NATS messaging server
- Kubernetes cluster (optional)
- Minimum 512MB RAM per instance

## Technical Stack

### Core Technologies
- **Language**: Go 1.24.1
- **Messaging**: NATS
- **Database**: PostgreSQL with GORM ORM
- **Container**: Docker with Kubernetes support

### Key Dependencies
- `github.com/quantumwake/alethic-ism-core-go`: Core ISM functionality
- `github.com/nats-io/nats.go`: NATS client library
- `gorm.io/gorm`: Database ORM
- `github.com/spf13/viper`: Configuration management

## Use Cases

This service is designed for:
- **Event Stream Processing**: Real-time correlation of events from multiple streams
- **Data Enrichment**: Joining complementary data from different sources
- **Session Reconstruction**: Correlating distributed session events
- **Microservices Integration**: Stateful stream processing in distributed architectures

## Performance Considerations

### Optimization Features
- Per-event TTL for precise memory management
- Efficient in-memory caching with granular automatic eviction
- Parallel processing capabilities
- Minimal data copying during join operations
- Performance statistics tracking
- Improved cache block partitioning for faster lookups
- Reduced memory footprint through aggressive expired event cleanup

### Scalability Notes
- Horizontal scaling supported through NATS partitioning
- Database connection pooling for high throughput
- Comments indicate future support for distributed caching

## Development

### Building the Service
```bash
go build -o state-online-join cmd/service/main.go
```

### Running Tests
```bash
go test ./...
```

### Docker Build
```bash
docker build -t alethic-ism-state-online-join .
```

## Monitoring

The service provides:
- Processing statistics (events processed, joins performed)
- Performance metrics (processing time, cache hit rates)
- Error tracking and logging
- Health check endpoints for orchestration

## Recent Enhancements

### Per-Event TTL Eviction (v2.0.0)
- Implemented granular per-event time-to-live management
- Added configurable TTL at global, route, and event levels
- Improved memory efficiency through precise eviction control
- Enhanced cache block partitioning with new `blockpart.go` module

## Future Enhancements

Based on code comments and structure:
- Distributed caching support for multi-instance deployments
- Advanced join strategies (outer joins, conditional joins)
- Enhanced performance monitoring and tracing
- Support for larger correlation windows
- Dynamic TTL adjustment based on system load
- Event priority-based eviction policies

## License

Alethic ISM is under a DUAL licensing model, please refer to [LICENSE.md](LICENSE.md).

**AGPL v3**
Intended for academic, research, and nonprofit institutional use. As long as all derivative works are also open-sourced under the same license, you are free to use, modify, and distribute the software.

**Commercial License**
Intended for commercial use, including production deployments and proprietary applications. This license allows for closed-source derivative works and commercial distribution. Please contact us for more information.

## Contributing

[Contributing guidelines to be added]