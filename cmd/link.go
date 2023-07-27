/*
Copyright © 2023 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"fmt"
	"os"

	kourai "github.com/alzabo/kourai/pkg"
	"github.com/spf13/cobra"
)

var (
	srcsDefault    []string = []string{"./"}
	dryRun         bool
	skipTitleCaser bool
)

// linkCmd represents the link command
var linkCmd = &cobra.Command{
	Use:   "link",
	Short: "A brief description of your command",
	Long: `A longer description that spans multiple lines and likely contains examples
and usage of using your command. For example:

Cobra is a CLI library for Go that empowers applications.
This application is a tool to generate the needed files
to quickly create a Cobra application.`,
	Run: func(cmd *cobra.Command, args []string) {
		key := cmd.Flags().Lookup("api-key").Value.String()
		dest := cmd.Flags().Lookup("dest").Value.String()

		fmt.Println("dry-run", dryRun)

		if len(args) == 0 {
			args = srcsDefault
		}

		link, err := kourai.LinkFromFiles(
			kourai.WithDestination(dest),
			kourai.WithSources(args),
			kourai.WithFileExtensions(extensions),
			kourai.WithFileModificationFilter(after, before),
			kourai.WithExcludePatterns(excludes),
			kourai.WithTMDBApiKey(key),
			kourai.WithoutTitleCaseModification(skipTitleCaser),
			kourai.WithExcludeTypes(excludeMovies, excludeTv),
			kourai.WithCountryFilter(excludeCountries),
		)
		if err != nil {
			fmt.Println("encountered error:", err)
			os.Exit(1)
		}

		for _, l := range link {
			if dryRun {
				fmt.Printf("%v\t%v\n", l.Src, l.Target)
			} else {
				l.Create()
			}
		}
	},
}

func init() {
	rootCmd.AddCommand(linkCmd)

	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	//linkCmd.PersistentFlags().StringSliceVarP(&srcs, "src", "s", srcsDefault, "directories to consider")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	linkCmd.Flags().StringP("dest", "d", "", "Destination directory")
	linkCmd.MarkFlagRequired("dest")

	linkCmd.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "Run without making any changes to files")
	linkCmd.Flags().BoolVarP(&skipTitleCaser, "keep-title-case", "k", false, "Don't alter title case")
}
