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

	st := newStore(p)
	totalRemoved := 0
	observationCount := 0
	if _, err := st.updateObservationsIfChanged(func(current []observation) ([]observation, bool, error) {
		observationCount = len(current)
		for i := range current {
			removed := dedupEvidence(&current[i])
			if removed > 0 {
				fmt.Printf("%s: %d duplicate evidence entries removed (now count=%d)\n",
					current[i].ID, removed, current[i].EvidenceCount)
				totalRemoved += removed
			}
		}
		return current, totalRemoved > 0, nil
	}); err != nil {
		return fmt.Errorf("writing observations: %w", err)
	}
	if observationCount == 0 {
		fmt.Println("nothing to compact (no observations yet)")
		return nil
	}
	if totalRemoved == 0 {
		fmt.Println("nothing to compact (no duplicates found)")
		return nil
	}
	fmt.Printf("\ncompacted: %d duplicates removed across %d observations\n",
		totalRemoved, observationCount)
	return nil
}
