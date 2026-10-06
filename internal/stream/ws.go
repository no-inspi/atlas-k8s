package stream

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/coder/websocket"
)

const (
	writeTimeout = 10 * time.Second
	pingInterval = 30 * time.Second
)

// Handler sert /api/stream. Le client peut passer ?rev=N pour reprendre là où
// il s'était arrêté. Chaque message JSON part dans sa propre frame texte.
// L'Origin est vérifiée par websocket.Accept (même hôte uniquement).
func Handler(h *Hub, log *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastRev, _ := strconv.ParseUint(r.URL.Query().Get("rev"), 10, 64)

		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			CompressionMode: websocket.CompressionContextTakeover,
		})
		if err != nil {
			log.Debug("stream: upgrade refusé", "err", err)
			return
		}
		defer c.CloseNow()

		// Le client n'envoie rien : CloseRead gère les frames de contrôle et
		// annule le contexte à la fermeture côté navigateur.
		ctx := c.CloseRead(r.Context())

		initial, sub := h.Subscribe(lastRev)
		defer sub.Close()

		if err := writeAll(ctx, c, initial); err != nil {
			return
		}
		h.CountSent(len(initial))
		ping := time.NewTicker(pingInterval)
		defer ping.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case batch, ok := <-sub.C:
				if !ok {
					c.Close(websocket.StatusTryAgainLater, "client trop lent, reconnectez-vous")
					return
				}
				if err := writeAll(ctx, c, batch); err != nil {
					return
				}
				h.CountSent(len(batch))
			case <-ping.C:
				pctx, cancel := context.WithTimeout(ctx, writeTimeout)
				err := c.Ping(pctx)
				cancel()
				if err != nil {
					return
				}
			}
		}
	})
}

func writeAll(ctx context.Context, c *websocket.Conn, msgs []Message) error {
	for _, m := range msgs {
		b, err := json.Marshal(m)
		if err != nil {
			return err
		}
		wctx, cancel := context.WithTimeout(ctx, writeTimeout)
		err = c.Write(wctx, websocket.MessageText, b)
		cancel()
		if err != nil {
			return err
		}
	}
	return nil
}
