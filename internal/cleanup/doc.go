// Package cleanup reduces a commit-message file the way git's own cleanup
// will before it records the message: ResolveMode answers which cleanup git
// applies from what a commit-msg hook can read, and Apply carries it out — a
// port of git's strbuf_stripspace and wt_status_locate_end, not an
// approximation of them.
//
// It is pure — no git, no I/O, no clock: the caller reads git's settings and
// the hook's environment and hands the answers in. It serves the authoring
// path only (the commit-msg hook's `lint --stdin` and doctor's replay of it);
// a message read back from git is already clean and must never pass through
// here again. The port answers to real git, never to a model of it: DESIGN §5
// names the oracles that hold it there.
package cleanup
