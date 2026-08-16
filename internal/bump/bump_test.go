package bump

import (
	"testing"
)

func TestReduce(t *testing.T) {
	cases := []struct {
		name   string
		levels []Level
		want   Level
	}{
		{"empty is none", nil, LevelNone},
		{"single", []Level{LevelPatch}, LevelPatch},
		{"none and patch", []Level{LevelNone, LevelPatch}, LevelPatch},
		{"minor beats patch", []Level{LevelPatch, LevelMinor, LevelNone}, LevelMinor},
		{"major beats all", []Level{LevelMinor, LevelMajor, LevelPatch}, LevelMajor},
		{"all none", []Level{LevelNone, LevelNone}, LevelNone},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Reduce(c.levels); got != c.want {
				t.Fatalf("Reduce(%v) = %s, want %s", c.levels, got, c.want)
			}
		})
	}
}

func FuzzReduceInvariants(f *testing.F) {
	f.Add([]byte{0, 1, 2, 3}, 1)
	f.Add([]byte{3, 3, 0}, 2)
	f.Add([]byte{}, 0)
	rungs := []Level{LevelNone, LevelPatch, LevelMinor, LevelMajor}
	f.Fuzz(func(t *testing.T, seed []byte, rot int) {
		levels := make([]Level, len(seed))
		for i, b := range seed {
			levels[i] = rungs[int(b)%len(rungs)]
		}
		want := Reduce(levels)

		reversed := make([]Level, len(levels))
		for i, l := range levels {
			reversed[len(levels)-1-i] = l
		}
		if got := Reduce(reversed); got != want {
			t.Fatalf("Reduce(reversed %v) = %s, want %s", levels, got, want)
		}

		if len(levels) > 0 {
			k := ((rot % len(levels)) + len(levels)) % len(levels)
			rotated := append(append([]Level{}, levels[k:]...), levels[:k]...)
			if got := Reduce(rotated); got != want {
				t.Fatalf("Reduce(rotated %v by %d) = %s, want %s", levels, k, got, want)
			}
		}

		if got := Reduce(append(append([]Level{}, levels...), want)); got != want {
			t.Fatalf("Reduce is not idempotent: folding %s back into %v gave %s", want, levels, got)
		}

		for n := range levels {
			if Reduce(levels[:n]).Rank() > want.Rank() {
				t.Fatalf("Reduce(%v[:%d]) exceeds Reduce of the whole", levels, n)
			}
		}
	})
}
