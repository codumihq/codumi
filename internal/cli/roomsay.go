package cli

// codumi_room_say: um agente de uma sala manda um recado para outro agente (ou
// para a sala toda). O recado vai para a caixa de entrada do app, que mostra
// para você entregar ou descartar (ou entrega sozinho, se a sala permitir).

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/codumihq/codumi/internal/store"
)

// RoomInbox: o arquivo onde os recados esperam o app (o mesmo diretório do app).
func RoomInbox() string {
	dir := os.Getenv("CODUMI_CONFIG_DIR")
	if dir == "" {
		d, err := os.UserConfigDir()
		if err != nil {
			d = os.TempDir()
		}
		dir = store.UserDir(d)
	}
	return filepath.Join(dir, "room-inbox.jsonl")
}

func roomSay(room, from, to, text string) error {
	room, from, to, text = strings.TrimSpace(room), strings.TrimSpace(from), strings.TrimSpace(to), strings.TrimSpace(text)
	if room == "" || from == "" || text == "" {
		return errors.New("room, from and text are required (they are in your room instructions)")
	}
	if to == "" {
		to = "all"
	}
	if len(text) > 4000 {
		text = text[:4000]
	}
	b, _ := json.Marshal(map[string]any{"room": room, "from": from, "to": to, "text": text, "at": time.Now().Unix()})
	os.MkdirAll(filepath.Dir(RoomInbox()), 0o755)
	f, err := os.OpenFile(RoomInbox(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(b, '\n')); err != nil {
		return err
	}
	fmt.Fprintln(out, "sent: the user sees it in the room and delivers it to "+to+". Keep working; an answer, if any, arrives as a new message.")
	return nil
}
