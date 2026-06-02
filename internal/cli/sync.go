package cli

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/CurtMeadows/straddler/internal/db"
	"github.com/CurtMeadows/straddler/internal/registry"
	"github.com/spf13/cobra"
)

func newSyncCmd(env *cmdEnv) *cobra.Command {
	var (
		source       string
		dest         string
		explicitTags string
		tagFilter    string
		dryRun       bool
		batchSize    int
	)

	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Fetch tags and enqueue copy jobs",
		Long: `Enumerate all tags for a source image and add them to the job queue.
Run 'straddler worker' to process the queue.

Examples:
  # Enqueue all nginx tags from Docker Hub to ECR
  straddler sync \
    --source docker.io/library/nginx \
    --dest   123456.dkr.ecr.us-east-1.amazonaws.com/nginx

  # Sync only 1.x tags from GHCR to Quay
  straddler sync \
    --source ghcr.io/myorg/myimage \
    --dest   quay.io/myorg/myimage \
    --tag-filter "^1\."

  # Preview without writing to the database
  straddler sync \
    --source docker.io/library/alpine \
    --dest   harbor.example.com/library/alpine \
    --tags   3.18,3.19,latest \
    --dry-run`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()

			// Compile the optional tag filter regex before doing any I/O so
			// a bad pattern fails fast without hitting the registry.
			var filter *regexp.Regexp
			if tagFilter != "" {
				var err error
				filter, err = regexp.Compile(tagFilter)
				if err != nil {
					return fmt.Errorf("invalid --tag-filter %q: %w", tagFilter, err)
				}
			}

			logger := env.logger.With("source", source, "dest", dest)

			// ── Build tag list ────────────────────────────────────────────────
			var tags []string

			if explicitTags != "" {
				for _, t := range strings.Split(explicitTags, ",") {
					if t = strings.TrimSpace(t); t != "" {
						tags = append(tags, t)
					}
				}
				logger.Info("using explicit tag list", "count", len(tags))
			} else {
				transport := registry.BuildTransport(env.cfg.Registry.InsecureSkipTLS)
				client := registry.NewRemoteClient(
					registry.BuildKeychain(env.cfg.Registry.Source),
					registry.BuildKeychain(env.cfg.Registry.Dest),
					transport,
				)

				logger.Info("fetching tags from source registry")
				var err error
				tags, err = client.ListTags(ctx, source)
				if err != nil {
					return fmt.Errorf("list tags for %q: %w", source, err)
				}
				logger.Info("fetched tags", "count", len(tags))
			}

			// ── Apply regex filter ────────────────────────────────────────────
			if filter != nil {
				before := len(tags)
				// Reslice to zero length but keep the backing array so we
				// filter in-place without a new allocation.
				filtered := tags[:0]
				for _, t := range tags {
					if filter.MatchString(t) {
						filtered = append(filtered, t)
					}
				}
				tags = filtered
				logger.Info("filtered tags", "before", before, "after", len(tags))
			}

			if len(tags) == 0 {
				logger.Info("no tags to enqueue")
				return nil
			}

			// ── Dry run ───────────────────────────────────────────────────────
			if dryRun {
				fmt.Printf("dry-run: would enqueue %d tags:\n", len(tags))
				for _, t := range tags {
					fmt.Printf("  %s:%s  →  %s:%s\n", source, t, dest, t)
				}
				return nil
			}

			// ── Enqueue in batches ────────────────────────────────────────────
			pool, err := db.Open(ctx,
				env.cfg.Database.DSN,
				env.cfg.Database.MaxConns,
				env.cfg.Database.MinConns,
				env.cfg.Database.ConnectTimeout,
			)
			if err != nil {
				return fmt.Errorf("connect to database: %w", err)
			}
			defer pool.Close()

			queue := db.NewQueue(pool)

			var totalInserted int64
			for start := 0; start < len(tags); start += batchSize {
				end := min(start+batchSize, len(tags))

				params := make([]db.EnqueueParams, end-start)
				for i, tag := range tags[start:end] {
					params[i] = db.EnqueueParams{
						SourceRef:   source + ":" + tag,
						DestRef:     dest + ":" + tag,
						MaxAttempts: env.cfg.Worker.MaxAttempts,
					}
				}

				n, err := queue.BulkEnqueue(ctx, params)
				if err != nil {
					return fmt.Errorf("enqueue batch: %w", err)
				}
				totalInserted += n
			}

			skipped := int64(len(tags)) - totalInserted
			logger.Info("enqueue complete",
				"enqueued", totalInserted,
				"skipped_duplicates", skipped,
			)
			fmt.Printf("Enqueued %d new jobs (%d already existed)\n", totalInserted, skipped)
			return nil
		},
	}

	cmd.Flags().StringVar(&source, "source", "", "source repository, e.g. docker.io/library/nginx (required)")
	cmd.Flags().StringVar(&dest, "dest", "", "destination repository, e.g. ghcr.io/myorg/nginx (required)")
	cmd.Flags().StringVar(&explicitTags, "tags", "", "comma-separated tags; skips registry enumeration")
	cmd.Flags().StringVar(&tagFilter, "tag-filter", "", `regex to filter enumerated tags, e.g. "^1\."`)
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print what would be enqueued without writing to the database")
	cmd.Flags().IntVar(&batchSize, "batch-size", 100, "number of jobs per INSERT transaction")

	_ = cmd.MarkFlagRequired("source")
	_ = cmd.MarkFlagRequired("dest")

	return cmd
}
