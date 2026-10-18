package recon

import (
	"fmt"
	"testing"
)

// scanIntsFormats are the formats the codecs pass to scanInts.
var scanIntsFormats = []string{"line %d, column %d:", "yaml: line %d:"}

func checkScanIntsAgainstSscanf(t *testing.T, format, input string) {
	t.Helper()
	var want, got [2]int
	wantArgs := []any{&want[0], &want[1]}
	gotArgs := []*int{&got[0], &got[1]}
	verbs := 2
	if format == "yaml: line %d:" {
		verbs = 1
	}
	_, err := fmt.Sscanf(input, format, wantArgs[:verbs]...)
	ok := scanInts(input, format, gotArgs[:verbs]...)
	if ok != (err == nil) {
		t.Fatalf("scanInts(%q, %q) = %v, Sscanf error %v", input, format, ok, err)
	}
	if ok && got != want {
		t.Fatalf("scanInts(%q, %q) read %v, Sscanf read %v", input, format, got, want)
	}
}

var scanIntsInputs = []string{
	"", "line", "line ", "line 3", "line 3,", "line 3, column 4", "line 3, column 4:",
	"line 3, column 4: found x", "line 3,column 4:", "line  3,   column\t4:", "line3, column 4:",
	"line +3, column -4:", "line -3, column 4:", "line +, column 4:", "line 0x3, column 4:",
	"line 3 , column 4:", "line \n3, column 4:", "line \r\n3, column 4:", "line \r3, column 4:",
	"line\n 3, column 4:", "line 3, column 4", "line 9223372036854775807, column 1:",
	"line 9223372036854775808, column 1:", "line 007, column 08:", "line 3_0, column 4:",
	"line 3, column 4:", "line  3, column 4:", "line \xff3, column 4:", "line 3\xff",
	"yaml: line 7:", "yaml: line 7: mapping values are not allowed", "yaml:line 7:",
	"yaml: line7:", "yaml:  line   7:", "yaml: line 7", "yaml: line -7:", "yaml: line : x",
	"Yaml: line 7:", "yaml: line 7 :", "yaml: line ٣:", " yaml: line 7:",
}

func TestScanIntsMatchesSscanf(t *testing.T) {
	for _, format := range scanIntsFormats {
		for _, input := range scanIntsInputs {
			checkScanIntsAgainstSscanf(t, format, input)
		}
	}
}

func FuzzScanInts(f *testing.F) {
	for _, input := range scanIntsInputs {
		f.Add(input)
	}
	f.Fuzz(func(t *testing.T, input string) {
		for _, format := range scanIntsFormats {
			checkScanIntsAgainstSscanf(t, format, input)
		}
	})
}
