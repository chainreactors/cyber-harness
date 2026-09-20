package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

type User struct {
	ID, Tenant, Role, Token string
	Active, CanRead         bool
}
type Order struct{ ID, Tenant, Secret string }
type Export struct{ ID, Owner, OrderID, Status, Content string }
type Seed struct {
	Users  []User
	Orders []Order
}
type app struct {
	db       *sql.DB
	audit    *os.File
	revision string
	mu       sync.Mutex
	workers  sync.WaitGroup
}

func uid() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func main() {
	addr := flag.String("addr", "127.0.0.1:0", "listen address")
	dbPath := flag.String("db", "orders.db", "SQLite file")
	seedPath := flag.String("seed", "seed.json", "initial users and orders")
	auditPath := flag.String("audit", "audit.jsonl", "HTTP audit output")
	revision := flag.String("revision", "unknown", "source digest")
	flag.Parse()
	a, err := openApp(*dbPath, *seedPath, *auditPath, *revision)
	if err != nil {
		log.Fatal(err)
	}
	defer a.db.Close()
	defer a.audit.Close()
	srv := &http.Server{Addr: *addr, Handler: a.handler(), ReadHeaderTimeout: 5 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
	a.workers.Wait()
}

func openApp(dbPath, seedPath, auditPath, revision string) (*app, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS users(id TEXT PRIMARY KEY, tenant TEXT, role TEXT, token TEXT UNIQUE, active BOOLEAN, can_read BOOLEAN);
CREATE TABLE IF NOT EXISTS orders(id TEXT PRIMARY KEY, tenant TEXT, secret TEXT);
CREATE TABLE IF NOT EXISTS exports(id TEXT PRIMARY KEY, owner TEXT, order_id TEXT, status TEXT, content TEXT);
CREATE TABLE IF NOT EXISTS faults(name TEXT PRIMARY KEY);`)
	if err != nil {
		db.Close()
		return nil, err
	}
	var count int
	if err = db.QueryRow("SELECT count(*) FROM users").Scan(&count); err != nil {
		db.Close()
		return nil, err
	}
	if count == 0 {
		raw, err := os.ReadFile(seedPath)
		if err != nil {
			db.Close()
			return nil, err
		}
		var seed Seed
		if err = json.Unmarshal(raw, &seed); err != nil {
			db.Close()
			return nil, err
		}
		for _, u := range seed.Users {
			if _, err = db.Exec("INSERT INTO users VALUES(?,?,?,?,?,?)", u.ID, u.Tenant, u.Role, u.Token, u.Active, u.CanRead); err != nil {
				db.Close()
				return nil, err
			}
		}
		for _, o := range seed.Orders {
			if _, err = db.Exec("INSERT INTO orders VALUES(?,?,?)", o.ID, o.Tenant, o.Secret); err != nil {
				db.Close()
				return nil, err
			}
		}
	}
	audit, err := os.OpenFile(auditPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		db.Close()
		return nil, err
	}
	return &app{db: db, audit: audit, revision: revision}, nil
}

func (a *app) actor(r *http.Request) (User, bool) {
	var u User
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	err := a.db.QueryRow("SELECT id,tenant,role,token,active,can_read FROM users WHERE token=?", token).Scan(&u.ID, &u.Tenant, &u.Role, &u.Token, &u.Active, &u.CanRead)
	return u, err == nil && u.Active
}
func (a *app) order(id string) (Order, error) {
	var o Order
	err := a.db.QueryRow("SELECT id,tenant,secret FROM orders WHERE id=?", id).Scan(&o.ID, &o.Tenant, &o.Secret)
	return o, err
}
func (a *app) job(id string) (Export, error) {
	var e Export
	err := a.db.QueryRow("SELECT id,owner,order_id,status,content FROM exports WHERE id=?", id).Scan(&e.ID, &e.Owner, &e.OrderID, &e.Status, &e.Content)
	return e, err
}
func (a *app) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := uid()
		actor, _ := a.actor(r)
		var resource Order
		var export Export
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) >= 2 && parts[0] == "orders" {
			resource, _ = a.order(parts[1])
		}
		if len(parts) >= 2 && parts[0] == "exports" {
			export, _ = a.job(parts[1])
			resource, _ = a.order(export.OrderID)
		}
		status, body := a.route(r)
		raw, err := json.Marshal(body)
		if err != nil {
			status = 500
			raw = []byte(`{"error":"encode"}`)
		}
		hash := sha256.Sum256(raw)
		a.mu.Lock()
		err = json.NewEncoder(a.audit).Encode(map[string]any{"request_id": id, "at": time.Now().UTC(), "method": r.Method, "path": r.URL.Path, "actor": actor.ID, "tenant": actor.Tenant, "role": actor.Role, "can_read": actor.CanRead, "order_id": resource.ID, "order_tenant": resource.Tenant, "export_owner": export.Owner, "status": status, "body_sha256": hex.EncodeToString(hash[:]), "revision": a.revision})
		a.mu.Unlock()
		if err != nil {
			http.Error(w, "audit unavailable", 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Request-ID", id)
		w.Header().Set("X-Revision", a.revision)
		if status == 503 {
			w.Header().Set("Retry-After", "1")
		}
		w.WriteHeader(status)
		_, _ = w.Write(raw)
	})
}
func failure(status int, reason string) (int, any) { return status, map[string]any{"error": reason} }
func (a *app) route(r *http.Request) (int, any) {
	path := strings.Trim(r.URL.Path, "/")
	parts := strings.Split(path, "/")
	if r.Method == "GET" {
		switch path {
		case "health":
			return 200, map[string]any{"ok": true}
		case "version":
			return 200, map[string]any{"revision": a.revision}
		case "docs":
			return 200, map[string]any{"service": "OrderLab", "policy": "Members read/export only their tenant orders when can_read=true. Tenant admins manage membership and tenant exports. Public docs are intentional.", "routes": []string{"GET /me", "GET /orders", "GET /orders/{id}", "GET /orders/{id}/summary", "POST /exports {order_id}", "GET /exports", "GET /exports/{id}", "GET /exports/{id}/download", "DELETE /exports/{id}", "GET /admin/users", "GET /admin/users/{id}", "PATCH /admin/users/{id} {can_read}", "GET /admin/exports", "GET /metrics"}}
		}
	}
	u, ok := a.actor(r)
	if !ok {
		return failure(401, "authentication required")
	}
	if path == "me" && r.Method == "GET" {
		u.Token = ""
		return 200, u
	}
	if path == "metrics" && r.Method == "GET" {
		if u.Role != "admin" {
			return failure(403, "admin only")
		}
		var n int
		_ = a.db.QueryRow("SELECT count(*) FROM orders WHERE tenant=?", u.Tenant).Scan(&n)
		return 200, map[string]int{"orders": n}
	}
	if parts[0] == "orders" && r.Method == "GET" {
		if len(parts) == 1 {
			if !u.CanRead {
				return failure(403, "read permission required")
			}
			rows, err := a.db.Query("SELECT id,tenant,secret FROM orders WHERE tenant=?", u.Tenant)
			if err != nil {
				return failure(500, err.Error())
			}
			defer rows.Close()
			out := []Order{}
			for rows.Next() {
				var o Order
				if err := rows.Scan(&o.ID, &o.Tenant, &o.Secret); err != nil {
					return failure(500, err.Error())
				}
				out = append(out, o)
			}
			return 200, out
		}
		o, err := a.order(parts[1])
		if err != nil {
			return failure(404, "order not found")
		}
		if len(parts) == 2 {
			if !allowOrder(u, o) {
				return failure(403, "order denied")
			}
			return 200, o
		}
		if len(parts) == 3 && parts[2] == "summary" {
			if u.Tenant != o.Tenant || !u.CanRead {
				return failure(403, "order denied")
			}
			return 200, map[string]string{"id": o.ID, "tenant": o.Tenant}
		}
	}
	if parts[0] == "exports" {
		if len(parts) == 1 && r.Method == "POST" {
			var in struct {
				OrderID string `json:"order_id"`
			}
			if json.NewDecoder(r.Body).Decode(&in) != nil {
				return failure(400, "invalid JSON")
			}
			o, err := a.order(in.OrderID)
			if err != nil {
				return failure(404, "order not found")
			}
			if u.Tenant != o.Tenant || !u.CanRead {
				return failure(403, "export denied")
			}
			e := Export{ID: uid(), Owner: u.ID, OrderID: o.ID, Status: "queued"}
			if _, err = a.db.Exec("INSERT INTO exports VALUES(?,?,?,?,?)", e.ID, e.Owner, e.OrderID, e.Status, ""); err != nil {
				return failure(500, err.Error())
			}
			a.workers.Add(1)
			go func() {
				defer a.workers.Done()
				time.Sleep(100 * time.Millisecond)
				_, _ = a.db.Exec("UPDATE exports SET status='running' WHERE id=?", e.ID)
				time.Sleep(200 * time.Millisecond)
				_, _ = a.db.Exec("UPDATE exports SET status='completed',content=? WHERE id=?", o.Secret, e.ID)
			}()
			return 202, e
		}
		if len(parts) == 1 && r.Method == "GET" {
			return a.exportList(u, false)
		}
		if len(parts) >= 2 {
			e, err := a.job(parts[1])
			if err != nil {
				return failure(404, "export not found")
			}
			o, err := a.order(e.OrderID)
			if err != nil {
				return failure(404, "order not found")
			}
			if len(parts) == 3 && parts[2] == "download" && r.Method == "GET" {
				if !allowDownload(u, e, o) {
					return failure(403, "download denied")
				}
				if e.Status != "completed" {
					return failure(409, "export pending")
				}
				return 200, map[string]string{"export_id": e.ID, "content": e.Content}
			}
			if e.Owner != u.ID && !(u.Role == "admin" && u.Tenant == o.Tenant) {
				return failure(403, "export owner required")
			}
			if len(parts) == 2 && r.Method == "GET" {
				result, err := a.db.Exec("INSERT OR IGNORE INTO faults(name) VALUES('poll-503')")
				if err != nil {
					return failure(500, err.Error())
				}
				n, _ := result.RowsAffected()
				if n == 1 {
					return failure(503, "temporary export worker unavailable")
				}
				e.Content = ""
				return 200, e
			}
			if len(parts) == 2 && r.Method == "DELETE" {
				_, err = a.db.Exec("DELETE FROM exports WHERE id=?", e.ID)
				if err != nil {
					return failure(500, err.Error())
				}
				return 200, map[string]bool{"deleted": true}
			}
		}
	}
	if parts[0] == "admin" {
		if u.Role != "admin" {
			return failure(403, "admin only")
		}
		if path == "admin/exports" && r.Method == "GET" {
			return a.exportList(u, true)
		}
		if path == "admin/users" && r.Method == "GET" {
			rows, err := a.db.Query("SELECT id,tenant,role,active,can_read FROM users WHERE tenant=?", u.Tenant)
			if err != nil {
				return failure(500, err.Error())
			}
			defer rows.Close()
			out := []User{}
			for rows.Next() {
				var v User
				if rows.Scan(&v.ID, &v.Tenant, &v.Role, &v.Active, &v.CanRead) != nil {
					return failure(500, "read user")
				}
				out = append(out, v)
			}
			return 200, out
		}
		if len(parts) == 3 && parts[1] == "users" {
			var v User
			err := a.db.QueryRow("SELECT id,tenant,role,active,can_read FROM users WHERE id=?", parts[2]).Scan(&v.ID, &v.Tenant, &v.Role, &v.Active, &v.CanRead)
			if err != nil {
				return failure(404, "user not found")
			}
			if v.Tenant != u.Tenant {
				return failure(403, "tenant denied")
			}
			if r.Method == "GET" {
				return 200, v
			}
			if r.Method == "PATCH" {
				var in struct {
					CanRead *bool `json:"can_read"`
				}
				if json.NewDecoder(r.Body).Decode(&in) != nil || in.CanRead == nil {
					return failure(400, "can_read required")
				}
				if _, err = a.db.Exec("UPDATE users SET can_read=? WHERE id=?", *in.CanRead, v.ID); err != nil {
					return failure(500, err.Error())
				}
				v.CanRead = *in.CanRead
				return 200, v
			}
		}
	}
	return failure(404, "route not found")
}
func (a *app) exportList(u User, admin bool) (int, any) {
	query := "SELECT e.id,e.owner,e.order_id,e.status FROM exports e JOIN orders o ON o.id=e.order_id WHERE o.tenant=?"
	args := []any{u.Tenant}
	if !admin {
		query += " AND e.owner=?"
		args = append(args, u.ID)
	}
	rows, err := a.db.Query(query, args...)
	if err != nil {
		return failure(500, err.Error())
	}
	defer rows.Close()
	out := []Export{}
	for rows.Next() {
		var e Export
		if rows.Scan(&e.ID, &e.Owner, &e.OrderID, &e.Status) != nil {
			return failure(500, "read export")
		}
		out = append(out, e)
	}
	return 200, out
}
