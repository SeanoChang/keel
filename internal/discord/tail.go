package discord

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/fsnotify/fsnotify"
)

type LogTailer struct {
	agentName       string
	dir             string
	channelID       string
	statusChannelID string
	session         *discordgo.Session
	offset          int64
	stop            chan struct{}
	once            sync.Once
}

func NewLogTailer(agentName, dir, channelID, statusChannelID string, session *discordgo.Session) *LogTailer {
	var offset int64
	if info, err := os.Stat(filepath.Join(dir, "log.md")); err == nil {
		offset = info.Size()
	}
	return &LogTailer{
		agentName:       agentName,
		dir:             dir,
		channelID:       channelID,
		statusChannelID: statusChannelID,
		session:         session,
		offset:          offset,
		stop:            make(chan struct{}),
	}
}

func (t *LogTailer) Start() {
	logPath := filepath.Join(t.dir, "log.md")
	goalsPath := filepath.Join(t.dir, "GOALS.md")

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Printf("[keel] %s: fsnotify error: %v", t.agentName, err)
		return
	}
	defer watcher.Close()

	if err := watcher.Add(t.dir); err != nil {
		log.Printf("[keel] %s: watch error on %s: %v", t.agentName, t.dir, err)
		return
	}

	for {
		select {
		case <-t.stop:
			return
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) {
				switch event.Name {
				case logPath:
					t.readNewLines(logPath)
				case goalsPath:
					t.checkGoalsEmpty(goalsPath)
				}
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			log.Printf("[keel] %s: watcher error: %v", t.agentName, err)
		}
	}
}

func (t *LogTailer) Stop() {
	t.once.Do(func() { close(t.stop) })
}

func (t *LogTailer) readNewLines(logPath string) {
	f, err := os.Open(logPath)
	if err != nil {
		return
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil || info.Size() <= t.offset {
		return
	}

	f.Seek(t.offset, 0)
	buf := make([]byte, info.Size()-t.offset)
	n, err := f.Read(buf)
	if err != nil || n == 0 {
		return
	}
	t.offset = info.Size()

	content := strings.TrimSpace(string(buf[:n]))
	if content == "" {
		return
	}

	if len(content) > 1900 {
		content = content[:1900] + "\n... (truncated)"
	}

	msg := "```\n" + content + "\n```"
	_, err = t.session.ChannelMessageSend(t.channelID, msg)
	if err != nil {
		log.Printf("[keel] %s: discord send error: %v", t.agentName, err)
	}

	t.sendStatus(content)
}

func (t *LogTailer) sendStatus(content string) {
	if t.statusChannelID == "" {
		return
	}
	ts := time.Now().Format("2006-01-02 15:04:05")
	prefix := fmt.Sprintf("[%s - %s]", ts, t.agentName)
	// Trim for status channel — keep it concise
	if len(content) > 1500 {
		content = content[:1500] + "\n... (truncated)"
	}
	msg := fmt.Sprintf("%s\n```\n%s\n```", prefix, content)
	if _, err := t.session.ChannelMessageSend(t.statusChannelID, msg); err != nil {
		log.Printf("[keel] %s: status channel send error: %v", t.agentName, err)
	}
}

func (t *LogTailer) checkGoalsEmpty(goalsPath string) {
	data, err := os.ReadFile(goalsPath)
	if err != nil {
		return
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		_, _ = t.session.ChannelMessageSend(t.channelID,
			"All goals complete for **"+t.agentName+"**.")
		t.sendStatus("All goals complete.")
	}
}
