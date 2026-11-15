// Command check-results makes JMeter assertion failures fail the harness.
package main

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
)

func check(r io.Reader) error {
	records, err := csv.NewReader(r).ReadAll()
	if err != nil {
		return err
	}
	if len(records) < 2 {
		return fmt.Errorf("JMeter produced no samples")
	}
	success := -1
	for i, name := range records[0] {
		if name == "success" {
			success = i
		}
	}
	if success < 0 {
		return fmt.Errorf("JMeter results have no success column")
	}
	failed := 0
	for _, row := range records[1:] {
		if row[success] != "true" {
			failed++
		}
	}
	if failed > 0 {
		return fmt.Errorf("JMeter: %d/%d samples failed", failed, len(records)-1)
	}
	fmt.Printf("JMeter: %d samples, zero failures\n", len(records)-1)
	return nil
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: check-results results.jtl")
		os.Exit(1)
	}
	f, err := os.Open(os.Args[1])
	if err == nil {
		defer f.Close()
		err = check(f)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
