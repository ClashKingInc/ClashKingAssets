package render

type ExportOptions struct {
	RenderScale int
}

func normalizeExportOptions(opts ExportOptions) ExportOptions {
	if opts.RenderScale <= 1 {
		opts.RenderScale = 1
	}
	return opts
}
