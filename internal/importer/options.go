package importer

import "pkt.systems/pslog"
import "net/url"

// Options describes import settings for OpenAPI/WSDL conversion.
type Options struct {
	Source           string
	OutputDir        string
	OutputFile       string
	CollectionName   string
	GroupBy          string // tags|path (openapi)
	Insecure         bool
	AllowRemoteRefs  bool
	AllowFileRefs    bool
	Type             string
	GenerateTests    bool
	GenerateTestsSet bool
	IncludePaths     []string
	// Strictness controls how deep/strict generated schema assertions should be.
	// Values: "loose", "standard" (default), "strict".
	Strictness string
	Logger     pslog.Logger
	// BaseLocation tracks the original spec location (file or URL) for ref resolution.
	BaseLocation *url.URL
}
