package distill

import (
	"flag"
	"fmt"
	"sort"
)

func runList(args []string) error {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
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
		fmt.Println("(no observations yet — run `distill extract`)")
		return nil
	}

	sort.Slice(obs, func(i, j int) bool {
		return obs[i].EvidenceCount > obs[j].EvidenceCount
	})

	fmt.Printf("%d observation(s):\n\n", len(obs))
	for _, o := range obs {
		contradicted := ""
		if n := len(o.ContradictedBy); n > 0 {
			contradicted = fmt.Sprintf(" !contradicted=%d", n)
		}
		promoted := ""
		if o.Status == statusPromotedToSkill || o.Status == statusPromotedClaudeMD {
			promoted = fmt.Sprintf(" →%s=%s", o.Status, o.PromotedTo)
		}
		ignored := ""
		if o.Status == statusIgnored {
			ignored = " (ignored)"
		}
		fmt.Printf("%s  [%s]  count=%d%s%s%s\n", o.ID, o.Type, o.EvidenceCount, contradicted, promoted, ignored)
		fmt.Printf("  %s\n", o.Claim)
		if n := len(o.Evidence); n > 0 {
			recent := o.Evidence[n-1]
			if recent.Quote != "" {
				q := recent.Quote
				if len(q) > 120 {
					q = q[:117] + "…"
				}
				short := recent.SessionID
				if len(short) > 8 {
					short = short[:8]
				}
				fmt.Printf("  %q  (%s)\n", q, short)
			}
		}
		fmt.Println()
	}
	return nil
}
