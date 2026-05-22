package distill

import (
	"flag"
	"fmt"
)

func runCompact(args []string) error {
	fs := flag.NewFlagSet("compact", flag.ExitOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}

	p, err := resolvePaths()
	if err != nil {
		return err
	}

	obs, err := readObservations(p.observationFile)
	if err != nil {
		return err
	}
	if len(obs) == 0 {
		fmt.Println("nothing to compact (no observations yet)")
		return nil
	}

	totalRemoved := 0
	for i := range obs {
		removed := dedupEvidence(&obs[i])
		if removed > 0 {
			fmt.Printf("%s: %d duplicate evidence entries removed (now count=%d)\n",
				obs[i].ID, removed, obs[i].EvidenceCount)
			totalRemoved += removed
		}
	}

	if totalRemoved == 0 {
		fmt.Println("nothing to compact (no duplicates found)")
		return nil
	}

	if err := writeObservations(p.observationFile, obs); err != nil {
		return fmt.Errorf("writing observations: %w", err)
	}
	fmt.Printf("\ncompacted: %d duplicates removed across %d observations\n",
		totalRemoved, len(obs))
	return nil
}
