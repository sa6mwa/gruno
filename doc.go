// Package gruno exposes a Go API for running Bruno `.bru` collections in-process.
//
// Quick start:
//
//		ctx := context.Background()
//		g, _ := gruno.New(ctx)
//		sum, _ := g.RunFolder(ctx, "sampledata", gruno.RunOptions{
//			EnvPath: "sampledata/environments/local.bru",
//		})
//
// Run a single case with inline vars:
//
//		sum, _ := g.RunFile(ctx, "cases/get_user.bru", gruno.RunOptions{
//			EnvPath: "environments/local.bru",
//			Vars:    map[string]string{"USER_ID": "123"},
//		})
//
// Hooks:
//
//		g, _ := gruno.New(ctx,
//			gruno.WithPreRequestHook(func(ctx context.Context, info gruno.HookInfo, req *http.Request, log pslog.Base) error {
//				req.Header.Set("X-Signature", sign(req))
//				return nil
//			}),
//			gruno.WithPostRequestHook(func(ctx context.Context, info gruno.HookInfo, res gruno.CaseResult, log pslog.Base) error {
//				if !res.Passed {
//					log.Warn("case failed", "file", info.FilePath, "err", res.ErrorText)
//				}
//				return nil
//			}),
//		)
//
// Data-driven runs:
//
//		sum, _ := g.RunFolder(ctx, "sampledata", gruno.RunOptions{
//			EnvPath:      "sampledata/environments/local.bru",
//			CSVFilePath:  "users.csv",      // or JSONFilePath
//			Parallel:     true,             // optional
//			IterationCount: 0,              // ignored when CSV/JSON present
//		})
//
// Transport knobs mirror the CLI:
//
//		custom := &http.Client{Timeout: 5 * time.Second}
//		g, _ := gruno.New(ctx, gruno.WithHTTPClient(custom))
//
//		sum, _ := g.RunFolder(ctx, "sampledata", gruno.RunOptions{
//			EnvPath: "sampledata/environments/local.bru",
//			Timeout: 10 * time.Second,
//		})
//
// The SDK keeps concrete types unexported; interaction happens through the
// Gruno interface plus RunOptions and result structs defined in this package.
package gruno
