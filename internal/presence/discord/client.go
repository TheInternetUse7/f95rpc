package discord

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Microsoft/go-winio"
	"github.com/google/uuid"

	"f95rpc/internal/config"
	"f95rpc/internal/presence"
)

const (
	OpcodeHandshake = 0
	OpcodeFrame     = 1
	OpcodeClose     = 2
	OpcodePing      = 3
	OpcodePong      = 4
)

type Client struct {
	clientID string
	cfg      *config.Config
	conn     net.Conn
	mu       sync.Mutex
	closed   bool
	active   *presence.ActiveGame
}

func NewClient(clientID string, cfg *config.Config) *Client {
	return &Client{
		clientID: clientID,
		cfg:      cfg,
	}
}

func (c *Client) Connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.clientID == "" {
		return fmt.Errorf("missing discord_client_id: please set your Discord Application ID in config.yaml or DISCORD_CLIENT_ID env var")
	}

	if c.conn != nil {
		return nil
	}

	for i := 0; i < 10; i++ {
		pipePath := fmt.Sprintf(`\\.\pipe\discord-ipc-%d`, i)
		conn, err := winio.DialPipe(pipePath, nil)
		if err == nil {
			c.conn = conn
			if err := c.handshake(); err != nil {
				c.conn.Close()
				c.conn = nil
				continue
			}
			return nil
		}
	}

	return fmt.Errorf("could not connect to any discord ipc pipe")
}

func (c *Client) handshake() error {
	payload, err := json.Marshal(map[string]interface{}{
		"v":         1,
		"client_id": c.clientID,
	})
	if err != nil {
		return err
	}

	if err := c.writePacket(OpcodeHandshake, payload); err != nil {
		return err
	}

	// Read handshake response
	op, resp, err := c.readPacket()
	if err != nil {
		return fmt.Errorf("read handshake response: %w", err)
	}
	if op == OpcodeClose {
		return fmt.Errorf("handshake rejected: %s", string(resp))
	}

	return nil
}

func (c *Client) writePacket(opcode int32, payload []byte) error {
	buf := new(bytes.Buffer)
	if err := binary.Write(buf, binary.LittleEndian, opcode); err != nil {
		return err
	}
	if err := binary.Write(buf, binary.LittleEndian, int32(len(payload))); err != nil {
		return err
	}
	if _, err := buf.Write(payload); err != nil {
		return err
	}

	if _, err := c.conn.Write(buf.Bytes()); err != nil {
		return err
	}
	return nil
}

func (c *Client) readPacket() (int32, []byte, error) {
	header := make([]byte, 8)
	if _, err := io.ReadFull(c.conn, header); err != nil {
		return 0, nil, err
	}

	opcode := int32(binary.LittleEndian.Uint32(header[0:4]))
	length := int32(binary.LittleEndian.Uint32(header[4:8]))

	payload := make([]byte, length)
	if _, err := io.ReadFull(c.conn, payload); err != nil {
		return 0, nil, err
	}

	return opcode, payload, nil
}

func (c *Client) readPacketWithTimeout(timeout time.Duration) (int32, []byte, error) {
	if c.conn == nil {
		return 0, nil, fmt.Errorf("connection closed")
	}
	_ = c.conn.SetReadDeadline(time.Now().Add(timeout))
	defer func() {
		if c.conn != nil {
			_ = c.conn.SetReadDeadline(time.Time{})
		}
	}()
	return c.readPacket()
}

func (c *Client) Sync(game *presence.ActiveGame) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.active = game
	if game == nil {
		return c.clearLocked()
	}

	if c.conn == nil {
		if err := c.connectLocked(); err != nil {
			return err
		}
	}

	activity := c.buildActivity(game)
	args := map[string]interface{}{
		"pid":      os.Getpid(),
		"activity": activity,
	}

	payload, err := json.Marshal(map[string]interface{}{
		"cmd":   "SET_ACTIVITY",
		"args":  args,
		"nonce": uuid.New().String(),
	})
	if err != nil {
		return err
	}

	if err := c.writePacket(OpcodeFrame, payload); err != nil {
		c.conn.Close()
		c.conn = nil
		return fmt.Errorf("send set_activity: %w", err)
	}

	// Drain response so named pipe buffer stays clean
	_, _, _ = c.readPacketWithTimeout(500 * time.Millisecond)

	return nil
}

func (c *Client) Clear() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.active = nil
	return c.clearLocked()
}

func (c *Client) clearLocked() error {
	if c.conn == nil {
		return nil
	}

	args := map[string]interface{}{
		"pid":      os.Getpid(),
		"activity": nil,
	}

	payload, _ := json.Marshal(map[string]interface{}{
		"cmd":   "SET_ACTIVITY",
		"args":  args,
		"nonce": uuid.New().String(),
	})

	_ = c.writePacket(OpcodeFrame, payload)
	_, _, _ = c.readPacketWithTimeout(200 * time.Millisecond)

	// Closing the IPC pipe triggers Discord's connection teardown,
	// guaranteeing the presence card is wiped immediately from the client UI.
	_ = c.conn.Close()
	c.conn = nil

	return nil
}

func (c *Client) connectLocked() error {
	if c.clientID == "" {
		return fmt.Errorf("missing discord_client_id: please set your Discord Application ID in config.yaml or DISCORD_CLIENT_ID env var")
	}
	for i := 0; i < 10; i++ {
		pipePath := fmt.Sprintf(`\\.\pipe\discord-ipc-%d`, i)
		conn, err := winio.DialPipe(pipePath, nil)
		if err == nil {
			c.conn = conn
			if err := c.handshake(); err != nil {
				c.conn.Close()
				c.conn = nil
				continue
			}
			return nil
		}
	}
	return fmt.Errorf("discord not running")
}

func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.closed = true
	// clearLocked already closes the pipe and sets c.conn to nil.
	return c.clearLocked()
}

func (c *Client) buildActivity(game *presence.ActiveGame) map[string]interface{} {
	vars := map[string]string{
		"title":             game.Title,
		"creator":           game.Creator,
		"engine":            game.Engine,
		"status":            game.Status,
		"rating":            strconv.FormatFloat(game.Rating, 'f', 1, 64),
		"installed_version": game.InstalledVersion,
	}

	nameTmpl := c.cfg.Templates.Name
	if nameTmpl == "" {
		nameTmpl = "{title}"
	}
	name := config.ExpandTemplate(nameTmpl, vars)
	if name == "" {
		name = game.Title
	}

	details := config.ExpandTemplate(c.cfg.Templates.Details, vars)
	state := config.ExpandTemplate(c.cfg.Templates.State, vars)
	largeText := config.ExpandTemplate(c.cfg.Templates.LargeText, vars)
	smallText := config.ExpandTemplate(c.cfg.Templates.SmallText, vars)

	largeImage := c.cfg.DefaultLogoURL
	if largeImage == "" {
		largeImage = "https://f95zone.to/assets/images/logo-sqw192.png"
	}
	if game.ShowCover && game.CoverURL != "" {
		largeImage = game.CoverURL
	}

	assets := map[string]interface{}{
		"large_image": largeImage,
		"large_text":  truncateString(largeText, 128),
	}
	if smallText != "" {
		assets["small_text"] = truncateString(smallText, 128)
	}

	statusDisplayType := 0 // 0 = NAME ("Playing <name>")
	if strings.EqualFold(c.cfg.DisplayType, "details") {
		statusDisplayType = 1 // 1 = DETAILS ("Playing <details>")
	}

	activity := map[string]interface{}{
		"name":                truncateString(name, 128),
		"type":                0, // 0 = Playing
		"status_display_type": statusDisplayType,
		"timestamps": map[string]interface{}{
			"start": game.StartedAt.UnixMilli(),
		},
		"assets": assets,
	}
	if details != "" {
		activity["details"] = truncateString(details, 128)
	}
	if state != "" {
		activity["state"] = truncateString(state, 128)
	}

	var buttons []map[string]string
	if c.cfg.ShowThreadButton && game.ThreadURL != "" {
		buttonLabel := c.cfg.Templates.ButtonLabel
		if buttonLabel == "" {
			buttonLabel = "View on F95zone"
		}
		buttonLabel = config.ExpandTemplate(buttonLabel, vars)
		if buttonLabel == "" {
			buttonLabel = "View on F95zone"
		}
		buttons = append(buttons, map[string]string{
			"label": truncateString(buttonLabel, 32),
			"url":   game.ThreadURL,
		})
	}

	if c.cfg.ShowRepoButton {
		repoURL := c.cfg.RepoButtonURL
		if repoURL == "" {
			repoURL = "https://github.com/TheInternetUse7/f95rpc"
		}
		repoLabel := c.cfg.RepoButtonLabel
		if repoLabel == "" {
			repoLabel = c.cfg.Templates.RepoButtonLabel
		}
		if repoLabel == "" {
			repoLabel = "GitHub"
		}
		repoLabel = config.ExpandTemplate(repoLabel, vars)
		if repoLabel == "" {
			repoLabel = "GitHub"
		}
		buttons = append(buttons, map[string]string{
			"label": truncateString(repoLabel, 32),
			"url":   repoURL,
		})
	}

	if len(buttons) > 2 {
		buttons = buttons[:2]
	}
	if len(buttons) > 0 {
		activity["buttons"] = buttons
	}

	return activity
}

func mapEngineAsset(engine string) string {
	e := strings.ToLower(engine)
	switch {
	case strings.Contains(e, "ren'py") || strings.Contains(e, "renpy"):
		return "renpy"
	case strings.Contains(e, "unity"):
		return "unity"
	case strings.Contains(e, "rpgm") || strings.Contains(e, "rpg maker"):
		return "rpgm"
	case strings.Contains(e, "unreal"):
		return "unreal"
	case strings.Contains(e, "html"):
		return "html"
	case strings.Contains(e, "wolf"):
		return "wolf"
	case strings.Contains(e, "kirikiri"):
		return "kirikiri"
	case strings.Contains(e, "qsp"):
		return "qsp"
	default:
		return "other"
	}
}

func truncateString(s string, maxLen int) string {
	if len(s) > maxLen {
		return s[:maxLen]
	}
	if len(s) < 2 && len(s) > 0 {
		return s + " " // Discord requires min 2 chars for activity strings
	}
	return s
}
