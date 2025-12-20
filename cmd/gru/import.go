package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"pkt.systems/gruno/internal/importer"
)

func newImportCmd() *cobra.Command {
	importCmd := &cobra.Command{
		Use:   "import",
		Short: "Import a collection from other formats (openapi, wsdl)",
	}

	openapi := &cobra.Command{
		Use:   "openapi",
		Short: "Import from OpenAPI/Swagger",
		RunE: func(cmd *cobra.Command, args []string) error {
			logger := loggerFromCmd(cmd)
			src, _ := cmd.Flags().GetString("source")
			outDir, _ := cmd.Flags().GetString("output")
			outFile, _ := cmd.Flags().GetString("output-file")
			name, _ := cmd.Flags().GetString("collection-name")
			groupBy, _ := cmd.Flags().GetString("group-by")
			insecure, _ := cmd.Flags().GetBool("insecure")
			allowRemoteRefs, _ := cmd.Flags().GetBool("allow-remote-refs")
			allowFileRefs, _ := cmd.Flags().GetBool("allow-file-refs")
			strictness, _ := cmd.Flags().GetString("strictness")
			disableTests, _ := cmd.Flags().GetBool("disable-test-generation")
			includePaths, _ := cmd.Flags().GetStringSlice("include-path")
			if src == "" {
				return fmt.Errorf("--source is required")
			}
			if outDir == "" && outFile == "" {
				return fmt.Errorf("either --output or --output-file is required")
			}
			opts := importer.Options{
				Source:           src,
				OutputDir:        outDir,
				OutputFile:       outFile,
				CollectionName:   name,
				GroupBy:          groupBy,
				Insecure:         insecure,
				AllowRemoteRefs:  allowRemoteRefs,
				AllowFileRefs:    allowFileRefs,
				Strictness:       strictness,
				Type:             "openapi",
				GenerateTests:    !disableTests,
				GenerateTestsSet: true,
				IncludePaths:     includePaths,
				Logger:           logger,
			}
			return importer.ImportOpenAPI(context.Background(), opts)
		},
	}
	wsdl := &cobra.Command{
		Use:   "wsdl",
		Short: "Import from WSDL",
		RunE: func(cmd *cobra.Command, args []string) error {
			logger := loggerFromCmd(cmd)
			src, _ := cmd.Flags().GetString("source")
			outDir, _ := cmd.Flags().GetString("output")
			outFile, _ := cmd.Flags().GetString("output-file")
			name, _ := cmd.Flags().GetString("collection-name")
			insecure, _ := cmd.Flags().GetBool("insecure")
			disableTests, _ := cmd.Flags().GetBool("disable-test-generation")
			if src == "" {
				return fmt.Errorf("--source is required")
			}
			if outDir == "" && outFile == "" {
				return fmt.Errorf("either --output or --output-file is required")
			}
			opts := importer.Options{
				Source:           src,
				OutputDir:        outDir,
				OutputFile:       outFile,
				CollectionName:   name,
				Insecure:         insecure,
				Type:             "wsdl",
				GenerateTests:    !disableTests,
				GenerateTestsSet: true,
				Logger:           logger,
			}
			return importer.ImportWSDL(context.Background(), opts)
		},
	}

	addLoggingFlags(importCmd.Flags())
	addLoggingFlags(openapi.Flags())
	addLoggingFlags(wsdl.Flags())

	for _, c := range []*cobra.Command{openapi, wsdl} {
		c.Flags().StringP("source", "s", "", "Path or URL to source file")
		c.Flags().StringP("output", "o", "", "Output directory for collection")
		c.Flags().StringP("output-file", "f", "", "Output JSON file instead of directory")
		c.Flags().StringP("collection-name", "n", "", "Name for the imported collection")
		c.Flags().Bool("insecure", false, "Skip TLS verification when fetching URL")
	}
	openapi.Flags().StringP("group-by", "g", "tags", "Group by tags|path")
	openapi.Flags().Bool("allow-remote-refs", false, "Allow following remote $refs inside the OpenAPI document")
	openapi.Flags().Bool("allow-file-refs", false, "Allow absolute/local file $refs (blocked by default for security)")
	openapi.Flags().Bool("disable-test-generation", false, "Skip generating response schema-based tests")
	openapi.Flags().String("strictness", "standard", "Schema assertion strictness: loose|standard|strict")
	openapi.Flags().StringSliceP("include-path", "i", nil, "Only import operations whose path starts with one of these prefixes (repeatable)")
	wsdl.Flags().Bool("disable-test-generation", false, "Skip generating response tests")

	importCmd.AddCommand(openapi, wsdl)
	return importCmd
}
