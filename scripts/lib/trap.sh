# shellcheck shell=sh
# trap.sh — the cleanup-and-re-raise idiom check.sh, mutations.sh and
# fleet-preflight.sh share. Sourced, never run.
#
# A handler that only cleans up does not stop the script: the shell runs it and
# CARRIES ON, so an interrupted run goes on to print a verdict about evidence
# its own cleanup deleted. Measured in fleet-preflight.sh: SIGTERM mid-walk
# deleted the evidence files, and the run printed a normal `✓ preflight:` line
# with the whole NOT MEASURED section missing and exited 0. So each signal runs
# the cleanup, drops its own trap and re-raises itself, and the script dies of
# that signal with no verdict line. HUP is in the set beside INT and TERM: a
# closed terminal sends it.

# reraise_on_signals <cleanup-fn> runs <cleanup-fn> on EXIT, and on HUP, INT
# and TERM runs it and re-raises the signal.
# shellcheck disable=SC2064 # the function and signal names expand now, $$ when the trap fires
reraise_on_signals() {
  trap "$1" EXIT
  for _reraise_sig in HUP INT TERM; do
    trap "$1; trap - $_reraise_sig; kill -$_reraise_sig \$\$" "$_reraise_sig"
  done
}
