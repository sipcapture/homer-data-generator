package gen

import "fmt"

// CompactOptions runs DuckLake maintenance on an existing catalog.
type CompactOptions struct {
	LakeConfig
	MaxCompactedFiles int
	FlushFirst        bool
}

// Compact flushes inlined data and merges adjacent parquet files.
func Compact(opts CompactOptions) error {
	lake, err := openLake(opts.LakeConfig)
	if err != nil {
		return err
	}
	defer lake.Close()

	if opts.FlushFirst {
		fmt.Println("Flushing inlined data...")
		if err := lake.FlushInlined(); err != nil {
			return fmt.Errorf("flush_inlined_data: %w", err)
		}
	}

	max := opts.MaxCompactedFiles
	if max <= 0 {
		max = 100
	}
	fmt.Printf("Merging adjacent files (max_compacted_files=%d)...\n", max)
	if err := lake.MergeAdjacent(max); err != nil {
		return fmt.Errorf("merge_adjacent_files: %w", err)
	}

	files, bytes, err := lake.ParquetStats()
	if err != nil {
		return err
	}
	fmt.Printf("Done: %d ducklake-*.parquet files, %.2f GiB on disk\n", files, float64(bytes)/(1<<30))
	return nil
}
