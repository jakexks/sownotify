package cmd

import (
	"os"
	"strings"

	"github.com/jakexks/sownotify/notifier"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var rootCmd = &cobra.Command{
	Use:   "sownotify",
	Short: "Send notifications to pushover when there's a new torrent",
	Long:  `Send notifications to pushover when there's a new torrent.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return notifier.Run()
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		log.Fatal().Err(err).Send()
	}
}

func init() {
	cobra.OnInitialize(initConfig)
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	rootCmd.PersistentFlags().StringP("feed-url", "f", "", "URL to RSS feed")
	rootCmd.PersistentFlags().StringP("pushover-app-token", "t", "", "Pushover app API token")
	rootCmd.PersistentFlags().StringP("pushover-recipient", "r", "", "Pushover recipient")
	rootCmd.PersistentFlags().Int64P("poll-interval", "p", 120, "Interval at which to poll RSS feed for new items (seconds)")

	if err := viper.BindPFlags(rootCmd.PersistentFlags()); err != nil {
		log.Fatal().Err(err).Send()
	}
	viper.SetDefault("poll-interval", int64(120))
}

func initConfig() {
	wd, err := os.Getwd()
	if err != nil {
		log.Error().Err(err).Send()
		os.Exit(1)
	}

	viper.AddConfigPath(wd)
	viper.SetConfigName("sownotify")
	viper.SetEnvPrefix("sownotify")
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err == nil {
		log.Info().Str("file", viper.ConfigFileUsed()).Msg("Loaded config")
	}
}
