# MeshCom UDP Logger

A high-performance Go service that captures UDP packets from MeshCom LoRa mesh network nodes and logs them to both MQTT and SQLite for monitoring, analysis, and integration with other systems.

## Overview

MeshCom is a LoRa-based mesh networking system used primarily by amateur radio operators for long-range, low-bandwidth communications. MeshCom nodes can broadcast network traffic via UDP packets, which this logger captures and persists for analysis, dashboards, and system integration.

This logger provides:
- **Real-time message capture** from MeshCom nodes broadcasting on UDP port 1799
- **Dual persistence**: simultaneous logging to MQTT topics and SQLite database
- **High reliability**: non-blocking architecture prevents packet loss during network/disk operations
- **Metadata enrichment**: adds timestamps, peer information, and raw JSON for comprehensive logging

## Features

### Core Functionality
- Listens for UDP datagrams on port 1799 (default MeshCom UDP port)
- Parses JSON-formatted MeshCom messages
- Enriches messages with reception metadata (timestamp, source IP, source port)
- Publishes messages to MQTT topics based on message type
- Stores all messages in a SQLite database with WAL mode for concurrent access
- Batched database commits for optimal write performance

### Technical Architecture
- **Non-blocking design**: UDP receiver uses buffered channels to prevent packet drops
- **Concurrent processing**: Separate goroutines handle MQTT publishing and database writes
- **Fast UDP handling**: Minimal processing in the UDP receive path ensures packet capture reliability
- **SQLite optimizations**: Write-Ahead Logging (WAL) mode and batch commits maximize throughput

## Prerequisites

- **Go 1.21 or later** for compilation
- **MeshCom node** configured to send UDP packets (see Configuration section)
- **MQTT broker** (optional, for real-time message streaming)
- **SQLite3** (embedded, no separate installation needed)

## Installation

### Building from Source

1. Clone the repository:
```bash
git clone https://github.com/audric/meshcom-udp-logger.git
cd meshcom-udp-logger
```

2. Build the binary:
```bash
go build -o meshcom-udp-logger meshcom-udp-logger.go
```

Or use the provided build script:
```bash
chmod +x meshcom-udp-logger.make.sh
./meshcom-udp-logger.make.sh
```

### Installing as a System Service

A systemd service file is provided for running the logger as a background service on Linux systems:

```bash
# Copy the binary to a system location
sudo cp meshcom-udp-logger /usr/local/bin/

# Copy the service file
sudo cp meshcom-udp-logger.service /etc/systemd/system/

# Reload systemd and enable the service
sudo systemctl daemon-reload
sudo systemctl enable meshcom-udp-logger
sudo systemctl start meshcom-udp-logger

# Check service status
sudo systemctl status meshcom-udp-logger
```

## Configuration

### MeshCom Node Setup

To send UDP packets from your MeshCom node, connect via serial terminal and execute:

```bash
--extudp on                    # Enable external UDP transmission
--extudpip <LOGGER_IP>         # Set destination IP to your logger host
```

For broadcast mode (if supported by your firmware):
```bash
--extudp on
--extudpip 255.255.255.255     # Broadcast to local network
```

**Note**: The MeshCom firmware uses UDP port 1799 by default and this is not configurable.

### Logger Configuration

Configuration is currently done via environment variables or command-line flags (see source code for details):

- `UDP_PORT`: UDP listening port (default: 1799)
- `MQTT_BROKER`: MQTT broker address (e.g., tcp://localhost:1883)
- `MQTT_TOPIC_PREFIX`: Prefix for MQTT topics (default: meshcom/)
- `SQLITE_DB_PATH`: Path to SQLite database file (default: meshcom.db)

## Usage

### Basic Usage

```bash
# Run with defaults (UDP port 1799, local SQLite database)
./meshcom-udp-logger

# Specify custom configuration
./meshcom-udp-logger -port 1799 -mqtt tcp://mqtt.local:1883 -db /var/lib/meshcom/data.db
```

### MQTT Topics

Messages are published to MQTT topics based on their type. The topic structure is:

```
<prefix>/<message_type>
```

Example topics:
- `meshcom/message` - Text messages
- `meshcom/position` - GPS position updates
- `meshcom/telemetry` - Node telemetry data
- `meshcom/status` - Status updates

### SQLite Database Schema

The logger creates a table with the following structure:

```sql
CREATE TABLE meshcom_messages (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    rx_timestamp TEXT NOT NULL,
    peer_ip TEXT NOT NULL,
    peer_port INTEGER NOT NULL,
    message_type TEXT,
    source_callsign TEXT,
    destination_callsign TEXT,
    message_content TEXT,
    raw_json TEXT NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
```

### Querying the Database

```bash
# Connect to the database
sqlite3 meshcom.db

# View recent messages
SELECT rx_timestamp, source_callsign, destination_callsign, message_content 
FROM meshcom_messages 
ORDER BY id DESC 
LIMIT 10;

# Count messages by type
SELECT message_type, COUNT(*) 
FROM meshcom_messages 
GROUP BY message_type;

# Messages from a specific callsign
SELECT * FROM meshcom_messages 
WHERE source_callsign = 'OE1ABC-11';
```

## Integration Examples

### Home Assistant Integration

Use the MQTT data to integrate with Home Assistant for notifications and automations:

```yaml
mqtt:
  - name: "MeshCom Last Message"
    state_topic: "meshcom/message"
    value_template: "{{ value_json.message_content }}"
    json_attributes_topic: "meshcom/message"
    json_attributes_template: "{{ value_json | tojson }}"
```

### Grafana Dashboard

Query the SQLite database using the SQLite datasource plugin to create visualizations:
- Message volume over time
- Active nodes and callsigns
- Message type distribution
- Geographic position tracking (if position data is logged)

### Custom Applications

Read from either MQTT (for real-time processing) or SQLite (for historical analysis):

```python
# Example: Python MQTT subscriber
import paho.mqtt.client as mqtt
import json

def on_message(client, userdata, msg):
    data = json.loads(msg.payload)
    print(f"Message from {data['source_callsign']}: {data['message_content']}")

client = mqtt.Client()
client.on_message = on_message
client.connect("localhost", 1883)
client.subscribe("meshcom/#")
client.loop_forever()
```

## Architecture Details

### Data Flow

```
MeshCom Node → UDP:1799 → UDP Receiver → Buffered Channel
                                              ↓
                                    ┌─────────┴─────────┐
                                    ↓                   ↓
                              MQTT Publisher    SQLite Writer
                                    ↓                   ↓
                              MQTT Broker        SQLite DB
```

### Design Rationale

The architecture prioritizes packet capture reliability:

1. **UDP receiver** immediately pushes received packets into a buffered channel with minimal processing
2. **Separate goroutines** handle slower I/O operations (MQTT publish, database writes)
3. **Channel buffer** absorbs temporary traffic bursts without dropping packets
4. **Batch commits** to SQLite reduce disk I/O overhead
5. **WAL mode** allows concurrent reads during writes

This design ensures that network latency or disk performance never causes packet loss at the UDP receiver level.

## Troubleshooting

### No packets being received

1. Verify MeshCom node configuration:
   ```bash
   # On the node serial console
   --extudp on
   --extudpip <YOUR_LOGGER_IP>
   ```

2. Check firewall rules:
   ```bash
   # Allow UDP 1799
   sudo ufw allow 1799/udp
   # Or for iptables
   sudo iptables -A INPUT -p udp --dport 1799 -j ACCEPT
   ```

3. Test with netcat:
   ```bash
   # Listen for UDP packets
   nc -ul 1799
   ```

### Database locked errors

If you see database locked errors, ensure:
- Only one instance of the logger is running
- WAL mode is enabled (done automatically by the logger)
- Sufficient disk space and proper permissions

### MQTT connection issues

Check MQTT broker connectivity:
```bash
# Install mosquitto clients
sudo apt-get install mosquitto-clients

# Test subscription
mosquitto_sub -h localhost -t "meshcom/#" -v
```

## Performance Considerations

- **Memory**: Approximately 10-50 MB depending on channel buffer size and message rate
- **CPU**: Minimal (< 1% on modern hardware for typical amateur radio traffic)
- **Disk I/O**: Depends on message rate; typical amateur radio usage is very light
- **Network**: Designed to handle bursts; sustained rates depend on channel buffer size

## Contributing

Contributions are welcome! Please feel free to submit issues or pull requests.

### Development Setup

```bash
# Clone and build
git clone https://github.com/audric/meshcom-udp-logger.git
cd meshcom-udp-logger
go mod init meshcom-udp-logger  # If go.mod doesn't exist
go build

# Run tests (if available)
go test ./...
```

## License

[Check the repository for license information]

## Related Projects

- [MeshCom](https://icssw.org/meshcom/) - Official MeshCom firmware and documentation
- [MeshCom-Client](https://github.com/dg9vh/MeshCom-Client) - Python-based MeshCom client
- [MeshCom-HA](https://github.com/DN9KGB/MeshCom-HA) - Home Assistant integration
- [MeshDash](https://github.com/dh5dan/meshdash) - Web-based MeshCom dashboard

## Support

For issues, questions, or feature requests, please use the GitHub issue tracker.

For general MeshCom support and community discussion, visit:
- [Institute of Citizen Science](https://icssw.org/en/)
- MeshCom user groups and forums

## Acknowledgments

Built for the amateur radio and MeshCom community. Thanks to all contributors to the MeshCom ecosystem.

---

**Note**: This is a community project and is not officially affiliated with the MeshCom firmware developers.
