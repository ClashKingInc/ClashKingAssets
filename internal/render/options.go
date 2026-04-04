package render

type ExportOptions struct {
	RenderScale             int
	IncludePrefixes         []string
	FileConcurrency         int
	Profile                 bool
	ProfileTopN             int
	SkipTinyOutputThreshold int
}

func normalizeExportOptions(opts ExportOptions) ExportOptions {
	if opts.RenderScale <= 1 {
		opts.RenderScale = 1
	}
	if opts.FileConcurrency <= 0 {
		opts.FileConcurrency = 1
	}
	if opts.ProfileTopN <= 0 {
		opts.ProfileTopN = 5
	}
	if opts.SkipTinyOutputThreshold < 0 {
		opts.SkipTinyOutputThreshold = 0
	}
	return opts
}
