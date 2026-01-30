package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	_ "modernc.org/sqlite"
)

type Record struct {
	RxTS     string                 `json:"rx_ts"`
	PeerIP   string                 `json:"peer_ip"`
	PeerPort int                    `json:"peer_port"`
	RawJSON  string                 `json:"raw_json"`
	Data     map[string]any         `json:"data"` // full parsed payload
	Type     string                 `json:"type"`
	Src      string                 `json:"src"`
	Dst      string                 `json:"dst"`
	MsgID    string                 `json:"msg_id"`
	Lat      *float64               `json:"lat,omitempty"`
	Lon      *float64               `json:"lon,omitempty"`
	Alt      *float64               `json:"alt,omitempty"`
	Batt     *float64               `json:"batt,omitempty"`
	Payload  *string                `json:"payload,omitempty"` // msg body if present
	Extra    map[string]interface{} `json:"-"`
}

func main() {
	var (
		bindAddr   = flag.String("bind", ":1799", "UDP bind address, e.g. :1799 or 0.0.0.0:1799")
		dbPath     = flag.String("db", "/opt/meshcom-udp-logger/meshcom-udp-logger.sqlite", "SQLite DB path")
		mqttURL    = flag.String("mqtt", "tcp://127.0.0.1:1883", "MQTT broker URL")
		mqttUser   = flag.String("mqtt-user", "", "MQTT username")
		mqttPass   = flag.String("mqtt-pass", "", "MQTT password")
		topicRoot  = flag.String("topic-root", "meshcom/rx", "MQTT topic root")
		mqttQos    = flag.Int("qos", 1, "MQTT QoS (0,1,2)")
		batchSize  = flag.Int("batch", 100, "SQLite batch insert size")
		flushEvery = flag.Duration("flush", 1*time.Second, "SQLite flush interval")
	)
	flag.Parse()

	ctx, cancel := signalContext()
	defer cancel()

	// Ensure db directory exists
	if err := os.MkdirAll(dirOf(*dbPath), 0o755); err != nil {
		log.Fatalf("mkdir db dir: %v", err)
	}

	db := mustOpenDB(*dbPath)
	defer db.Close()
	mustInitDB(db)

	mc := mustConnectMQTT(*mqttURL, *mqttUser, *mqttPass)
	defer mc.Disconnect(1000)

	// Buffered channel to absorb bursts
	in := make(chan Record, 5000)

	// Fan-out: each sink gets its own channel
	toDB := make(chan Record, 5000)
	toMQTT := make(chan Record, 5000)

	// Dispatcher
	go func() {
		defer close(toDB)
		defer close(toMQTT)
		for r := range in {
			// non-blocking-ish fan-out; if a sink is slow, it backpressures via buffer
			toDB <- r
			toMQTT <- r
		}
	}()

	go sqliteWriter(ctx, db, toDB, *batchSize, *flushEvery)
	go mqttPublisher(ctx, mc, toMQTT, *topicRoot, byte(*mqttQos))

	log.Printf("Listening UDP on %s; writing DB %s; publishing MQTT %s", *bindAddr, *dbPath, *mqttURL)
	if err := udpLoop(ctx, *bindAddr, in); err != nil {
		log.Fatalf("udp loop error: %v", err)
	}
}

func udpLoop(ctx context.Context, bind string, out chan<- Record) error {
	pc, err := net.ListenPacket("udp", bind)
	if err != nil {
		return err
	}
	defer pc.Close()

	buf := make([]byte, 64*1024)

	go func() {
		<-ctx.Done()
		_ = pc.Close()
	}()

	for {
		n, addr, err := pc.ReadFrom(buf)
		if err != nil {
			// closed due to context cancel is normal
			select {
			case <-ctx.Done():
				close(out)
				return nil
			default:
				return err
			}
		}

		raw := strings.TrimSpace(string(buf[:n]))
		rx := time.Now().UTC().Format(time.RFC3339Nano)

		peerIP, peerPort := parsePeer(addr)

		// Parse JSON
		var m map[string]any
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			// Store as "invalid" record but keep raw
			out <- Record{
				RxTS:     rx,
				PeerIP:   peerIP,
				PeerPort: peerPort,
				RawJSON:  raw,
				Data: map[string]any{
					"type":   "invalid",
					"error":  err.Error(),
					"rawlen": len(raw),
				},
				Type: "invalid",
			}
			continue
		}

		rec := normalize(rx, peerIP, peerPort, raw, m)
		out <- rec
	}
}

func normalize(rx, ip string, port int, raw string, m map[string]any) Record {
	getStr := func(k string) string {
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok {
				return s
			}
		}
		return ""
	}
	getF := func(k string) *float64 {
		if v, ok := m[k]; ok {
			switch t := v.(type) {
			case float64:
				return &t
			}
		}
		return nil
	}

	t := getStr("type")
	src := getStr("src")
	dst := getStr("dst")
	msgID := getStr("msgid") // spec uses "msgid" in examples
	if msgID == "" {
		msgID = getStr("msg_id")
	}

	var payload *string
	if s := getStr("msg"); s != "" {
		payload = &s
	}

	// positions: lat/lon/alt; telemetry: batt, etc.
	lat := getF("lat")
	lon := getF("lon")
	alt := getF("alt")
	batt := getF("batt")

	return Record{
		RxTS:     rx,
		PeerIP:   ip,
		PeerPort: port,
		RawJSON:  raw,
		Data:     m,
		Type:     t,
		Src:      src,
		Dst:      dst,
		MsgID:    msgID,
		Lat:      lat,
		Lon:      lon,
		Alt:      alt,
		Batt:     batt,
		Payload:  payload,
	}
}

func mqttPublisher(ctx context.Context, c mqtt.Client, in <-chan Record, root string, qos byte) {
	for {
		select {
		case <-ctx.Done():
			return
		case r, ok := <-in:
			if !ok {
				return
			}
			// Topic: meshcom/rx/<type>
			topic := fmt.Sprintf("%s/%s", strings.TrimRight(root, "/"), safeTopic(r.Type))
			b, _ := json.Marshal(r) // should never fail for our struct
			tok := c.Publish(topic, qos, false, b)
			// don't block forever
			if !tok.WaitTimeout(5 * time.Second) {
				log.Printf("mqtt publish timeout topic=%s", topic)
				continue
			}
			if err := tok.Error(); err != nil {
				log.Printf("mqtt publish error topic=%s err=%v", topic, err)
			}
		}
	}
}

func sqliteWriter(ctx context.Context, db *sql.DB, in <-chan Record, batchSize int, flushEvery time.Duration) {
	ticker := time.NewTicker(flushEvery)
	defer ticker.Stop()

	type row struct {
		r Record
	}
	batch := make([]row, 0, batchSize)

	flush := func() {
		if len(batch) == 0 {
			return
		}
		tx, err := db.Begin()
		if err != nil {
			log.Printf("db begin: %v", err)
			batch = batch[:0]
			return
		}
		stmt, err := tx.Prepare(`
			INSERT INTO rx_messages
			(rx_ts, peer_ip, peer_port, type, src, dst, msg_id, lat, lon, alt, batt, payload, raw_json)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`)
		if err != nil {
			log.Printf("db prepare: %v", err)
			_ = tx.Rollback()
			batch = batch[:0]
			return
		}
		for _, it := range batch {
			r := it.r
			_, err := stmt.Exec(
				r.RxTS, r.PeerIP, r.PeerPort, nz(r.Type), nz(r.Src), nz(r.Dst), nz(r.MsgID),
				r.Lat, r.Lon, r.Alt, r.Batt, r.Payload, r.RawJSON,
			)
			if err != nil {
				log.Printf("db insert: %v", err)
			}
		}
		_ = stmt.Close()
		if err := tx.Commit(); err != nil {
			log.Printf("db commit: %v", err)
		}
		batch = batch[:0]
	}

	for {
		select {
		case <-ctx.Done():
			flush()
			return
		case <-ticker.C:
			flush()
		case r, ok := <-in:
			if !ok {
				flush()
				return
			}
			batch = append(batch, row{r: r})
			if len(batch) >= batchSize {
				flush()
			}
		}
	}
}

func mustOpenDB(path string) *sql.DB {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	// Some sane limits
	db.SetMaxOpenConns(1) // single writer, avoids SQLITE_BUSY issues
	db.SetMaxIdleConns(1)
	return db
}

func mustInitDB(db *sql.DB) {
	stmts := []string{
		`PRAGMA journal_mode=WAL;`,
		`PRAGMA synchronous=NORMAL;`,
		`CREATE TABLE IF NOT EXISTS rx_messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			rx_ts TEXT NOT NULL,
			peer_ip TEXT,
			peer_port INTEGER,
			type TEXT,
			src TEXT,
			dst TEXT,
			msg_id TEXT,
			lat REAL,
			lon REAL,
			alt REAL,
			batt REAL,
			payload TEXT,
			raw_json TEXT NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_rx_ts ON rx_messages(rx_ts);`,
		`CREATE INDEX IF NOT EXISTS idx_type_ts ON rx_messages(type, rx_ts);`,
		`CREATE INDEX IF NOT EXISTS idx_src_ts ON rx_messages(src, rx_ts);`,
		`CREATE INDEX IF NOT EXISTS idx_msg_id ON rx_messages(msg_id);`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			log.Fatalf("init db failed: %v (stmt=%s)", err, s)
		}
	}
}

func mustConnectMQTT(url, user, pass string) mqtt.Client {
	opts := mqtt.NewClientOptions()
	opts.AddBroker(url)
	opts.SetClientID(fmt.Sprintf("meshcom-udp-logger-%d", time.Now().UnixNano()))
	opts.SetAutoReconnect(true)
	opts.SetConnectRetry(true)
	opts.SetConnectRetryInterval(3 * time.Second)
	if user != "" {
		opts.SetUsername(user)
		opts.SetPassword(pass)
	}
	c := mqtt.NewClient(opts)
	tok := c.Connect()
	if !tok.WaitTimeout(10 * time.Second) {
		log.Fatalf("mqtt connect timeout")
	}
	if err := tok.Error(); err != nil {
		log.Fatalf("mqtt connect: %v", err)
	}
	return c
}

func parsePeer(addr net.Addr) (string, int) {
	ua, ok := addr.(*net.UDPAddr)
	if !ok || ua == nil {
		return "", 0
	}
	return ua.IP.String(), ua.Port
}

func safeTopic(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "unknown"
	}
	s = strings.ToLower(s)
	s = strings.Map(func(r rune) rune {
		// allow a-z0-9-_ only
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, s)
	return s
}

func nz(s string) *string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return &s
}

func dirOf(path string) string {
	i := strings.LastIndex(path, "/")
	if i <= 0 {
		return "."
	}
	return path[:i]
}

func signalContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan os.Signal, 2)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-ch
		cancel()
		<-ch
		os.Exit(1)
	}()
	return ctx, cancel
}
