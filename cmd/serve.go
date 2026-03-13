package cmd

import (
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/SeanoChang/keel/internal/config"
	"github.com/SeanoChang/keel/internal/discord"
	"github.com/spf13/cobra"
)

var (
	serveConfigPath  string
	serveSleep       time.Duration
	serveArchiveEvery int
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the Discord bot and agent loops",
	RunE:  runServe,
}

func expandPath(path string) string {
	if after, ok := strings.CutPrefix(path, "~/"); ok {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, after)
	}
	return path
}

func runServe(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load(serveConfigPath)
	if err != nil {
		return err
	}

	// Expand ~ in agent dirs
	for name, ch := range cfg.Channels {
		ch.AgentDir = expandPath(ch.AgentDir)
		cfg.Channels[name] = ch
	}

	bot, err := discord.NewBot(cfg, serveSleep, serveArchiveEvery)
	if err != nil {
		return err
	}

	if err := bot.Start(); err != nil {
		return err
	}

	log.Printf("[keel] serving — press Ctrl+C to stop")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	log.Printf("[keel] shutting down...")
	bot.Stop()
	return nil
}
