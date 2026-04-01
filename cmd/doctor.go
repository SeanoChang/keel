package cmd

import (
	"os"

	"github.com/SeanoChang/keel/internal/doctor"
	"github.com/spf13/cobra"
)

var (
	doctorConfigPath string
	doctorAgent      string
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Run health checks on system and agents",
	RunE:  runDoctor,
}

func runDoctor(cmd *cobra.Command, args []string) error {
	if !doctor.Run(doctorConfigPath, doctorAgent) {
		os.Exit(1)
	}
	return nil
}
