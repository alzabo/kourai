/*
Copyright © 2023 Ryan White
*/
package cmd

import (
	goflag "flag"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	cfgFile           string
	extensions        []string
	extensionsDefault []string = []string{"avi", "mkv", "mp4"}
	excludes          []string
	before            *time.Time
	after             *time.Time
	excludeTv         bool
	excludeMovies     bool
	excludeCountries  []string
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "kourai",
	Short: "A brief description of your application",
	Long: `A longer description that spans multiple lines and likely contains
examples and usage of using your application. For example:

Cobra is a CLI library for Go that empowers applications.
This application is a tool to generate the needed files
to quickly create a Cobra application.`,
	// Uncomment the following line if your bare application
	// has an action associated with it:
	// Run: func(cmd *cobra.Command, args []string) { },
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func timeFlagHelper(v string) (time.Time, error) {
	var t time.Time
	var err error

	formats := []string{"2006-01-02", "1/2", "1-2", "01/02", "01/02"}

	for _, f := range formats {
		t, err = time.Parse(f, v)
		if err != nil {
			continue
		}
		if t.Year() == 0 {
			t = t.AddDate(time.Now().Year(), 0, 0)
		}
		break
	}
	return t, err
}

func init() {
	cobra.OnInitialize(initConfig)

	goflag.Func("before", "Only consider files modified before the given date", func(d string) error {
		t, err := timeFlagHelper(d)
		if err != nil {
			return err
		}
		before = &t
		return nil
	})

	goflag.Func("after", "Only consider files modified after the given date", func(d string) error {
		t, err := timeFlagHelper(d)
		if err != nil {
			return err
		}
		after = &t
		return nil
	})

	// Add goflags to Cobra command
	rootCmd.PersistentFlags().AddGoFlagSet(goflag.CommandLine)

	// Here you will define your flags and configuration settings.
	// Cobra supports persistent flags, which, if defined here,
	// will be global for your application.

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is $HOME/.kourai.yaml)")
	rootCmd.PersistentFlags().StringSliceVarP(&extensions, "extensions", "e", extensionsDefault, "File extensions to consider (case-insensitive)")
	rootCmd.PersistentFlags().StringSliceVarP(&excludes, "exclude", "x", []string{}, "Patterns to Exclude")
	rootCmd.PersistentFlags().StringSliceVar(&excludeCountries, "exclude-countries", []string{}, "Origin countries to Exclude")
	rootCmd.PersistentFlags().String("api-key", "", "TMDB API Key")
	rootCmd.PersistentFlags().BoolVar(&excludeTv, "no-tv", false, "Exclude TV files and results")
	rootCmd.PersistentFlags().BoolVar(&excludeMovies, "no-movies", false, "Exclude Movie files and results")
}

// initConfig reads in config file and ENV variables if set.
func initConfig() {
	if cfgFile != "" {
		// Use config file from the flag.
		viper.SetConfigFile(cfgFile)
	} else {
		// Find home directory.
		home, err := os.UserHomeDir()
		cobra.CheckErr(err)

		// Search config in home directory with name ".kourai" (without extension).
		viper.AddConfigPath(home)
		viper.SetConfigType("yaml")
		viper.SetConfigName(".kourai")
	}

	viper.AutomaticEnv() // read in environment variables that match

	// If a config file is found, read it in.
	if err := viper.ReadInConfig(); err == nil {
		fmt.Fprintln(os.Stderr, "Using config file:", viper.ConfigFileUsed())
	}
}
