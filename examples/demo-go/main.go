// A tiny Go API used to test codumi: routes behind middleware, SQL, and
// two webhooks (one verified with HMAC, one not).
package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"io"
	"net/http"
	"os"
)

type Server struct{ db *sql.DB }

func (s *Server) routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("GET /v1/orders/{id}", s.auth(s.getOrder))
	mux.HandleFunc("POST /v1/webhooks/pix", s.pixWebhook)
	mux.HandleFunc("POST /v1/webhooks/signed", s.signedWebhook)
	return mux
}

func (s *Server) auth(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	})
}

func (s *Server) getOrder(w http.ResponseWriter, r *http.Request) {
	var status string
	s.db.QueryRow(`SELECT status FROM orders WHERE id = $1`, r.PathValue("id")).Scan(&status)
	io.WriteString(w, status)
}

// Deliberately unverified: anyone can mark an order as paid.
func (s *Server) pixWebhook(w http.ResponseWriter, r *http.Request) {
	markPaid(s.db, r.FormValue("order"))
}

func (s *Server) signedWebhook(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	mac := hmac.New(sha256.New, []byte(os.Getenv("WEBHOOK_SECRET")))
	mac.Write(body)
	want := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(r.Header.Get("X-Signature"))) {
		http.Error(w, "bad signature", http.StatusBadRequest)
		return
	}
	markPaid(s.db, r.URL.Query().Get("order"))
}

func markPaid(db *sql.DB, id string) {
	db.Exec(`UPDATE orders SET status = 'paid' WHERE id = $1`, id)
	db.Exec(`INSERT INTO payment_events (order_id) VALUES ($1)`, id)
}

func main() {
	s := &Server{}
	http.ListenAndServe(":8080", s.routes())
}
