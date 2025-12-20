package gruno

import (
	"context"

	"pkt.systems/gruno/internal/importer"
	"pkt.systems/pslog"
)

// ImportOptions control import of external specs into Bruno collections.
type ImportOptions struct {
	Source          string
	OutputDir       string
	OutputFile      string
	CollectionName  string
	GroupBy         string // for openapi: tags|path
	Insecure        bool
	AllowRemoteRefs bool
	AllowFileRefs   bool
	DisableTests    bool
	IncludePaths    []string
	Logger          pslog.Logger
}

// ImportOpenAPI generates a Bruno collection from an OpenAPI/Swagger spec.
func ImportOpenAPI(ctx context.Context, opts ImportOptions) error {
	return importer.ImportOpenAPI(ctx, importer.Options{
		Source:          opts.Source,
		OutputDir:       opts.OutputDir,
		OutputFile:      opts.OutputFile,
		CollectionName:  opts.CollectionName,
		GroupBy:         opts.GroupBy,
		Insecure:        opts.Insecure,
		AllowRemoteRefs: opts.AllowRemoteRefs,
		AllowFileRefs:   opts.AllowFileRefs,
		Type:            "openapi",
		GenerateTests:   !opts.DisableTests,
		GenerateTestsSet: true,
		IncludePaths:    opts.IncludePaths,
		Logger:          opts.Logger,
	})
}

// ImportWSDL generates a placeholder Bruno collection from a WSDL (best-effort).
func ImportWSDL(ctx context.Context, opts ImportOptions) error {
	return importer.ImportWSDL(ctx, importer.Options{
		Source:         opts.Source,
		OutputDir:      opts.OutputDir,
		OutputFile:     opts.OutputFile,
		CollectionName: opts.CollectionName,
		Insecure:       opts.Insecure,
		Type:           "wsdl",
		GenerateTests:  !opts.DisableTests,
		GenerateTestsSet: true,
	})
}
