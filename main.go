package main

import (
	"context"
	_ "github.com/mattn/go-sqlite3"
	"github.com/spf13/cobra"
	"log"
	"net/http"
	"time"
)

func main() {
	// start mem profile
	go func() {
		log.Println(http.ListenAndServe("localhost:6060", nil))
	}()

	rootCmd := &cobra.Command{
		Use:   "go-template-streaming",
		Short: "Simple demonstration of streaming from a DB into a template",
	}

	createCmd := &cobra.Command{
		Use:   "create",
		Short: "Create a test database",
		Run: func(cmd *cobra.Command, args []string) {
			totalUsers, _ := cmd.Flags().GetInt("total-users")
			err := createDb(totalUsers)
			cobra.CheckErr(err)
		},
	}

	createCmd.Flags().Int("total-users", 1000000, "Total number of users to insert into the database")

	generateCmd := &cobra.Command{
		Use:   "generate",
		Short: "Generate a markdown table from the test database",
		Run: func(cmd *cobra.Command, args []string) {
			var err error
			streaming, _ := cmd.Flags().GetBool("streaming")
			streamingString, _ := cmd.Flags().GetBool("streaming-string")
			triggerGC, _ := cmd.Flags().GetBool("trigger-gc")

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			go monitorHeapSize(ctx, triggerGC)

			start := time.Now()
			if streamingString {
				err = generateStreamingStringMarkdown()
			} else if streaming {
				err = generateStreamingMarkdown()
			} else {
				err = generateMarkdown()
			}
			elapsed := time.Since(start)
			log.Printf("Time elapsed: %s\n", elapsed)
			cobra.CheckErr(err)
		},
	}

	generateCmd.Flags().Bool("streaming", false, "Whether to stream the data from the DB or not")
	generateCmd.Flags().Bool("streaming-string", false, "Whether to stream the data from the DB as a string or not")
	generateCmd.Flags().Bool("trigger-gc", false, "Whether to trigger a GC before measuring max heap")

	rootCmd.AddCommand(createCmd)
	rootCmd.AddCommand(generateCmd)

	err := rootCmd.Execute()
	cobra.CheckErr(err)
}
