

## Architecture (Go)

- UDP receiver (net.ListenPacket) reads datagrams on :1799
- Parse JSON, add metadata: rx_ts, peer_ip, peer_port, raw_json
- Push into a buffered channel
- Two go routines consume:
-- MQTT publisher (topic by type)
-- SQLite writer (single writer; WAL; batch commits)

This keeps UDP handling fast and prevents disk/network stalls from dropping packets.

Compile with GOlang 1.21 or later
