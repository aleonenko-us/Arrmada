#!/bin/sh
# Prints one version's section of CHANGELOG.md: the lines under "## [0.18.0] - <date>" up to
# the next "## " heading (or the link list at the bottom), without the heading itself and
# without blank lines at either end. The Release workflow uses it for the GitHub release's
# notes; run it before tagging to see what the release will say:
#
#   scripts/changelog-section.sh v0.18.0            # reads ./CHANGELOG.md
#   scripts/changelog-section.sh 0.18.0 path/to/CHANGELOG.md
#
# Exits 1, printing nothing on stdout, when the file has no section for that version or the
# section is empty, so a release never goes out with notes missing by accident.
set -eu

if [ $# -lt 1 ] || [ $# -gt 2 ]; then
  echo "usage: $0 <version, e.g. v0.18.0> [CHANGELOG.md]" >&2
  exit 2
fi

version=${1#v}
file=${2:-CHANGELOG.md}

if [ -z "$version" ]; then
  echo "changelog-section: no version given" >&2
  exit 2
fi
if [ ! -f "$file" ]; then
  echo "changelog-section: $file not found" >&2
  exit 1
fi

# The heading is compared as plain text (index), not as a pattern, so the dots in a version
# can't match other characters. Lines are held back while blank, so trailing blank lines
# before the next heading are dropped and leading ones never start the output.
awk -v want="## [$version]" '
  function is_heading(line) { return substr(line, 1, 3) == "## " }
  # Keep a Changelog ends with link definitions: "[0.18.0]: https://...".
  function is_linkdef(line) { return line ~ /^\[[^]]+\]: / }
  {
    sub(/\r$/, "")
    if (done) next
    if (!inside) {
      if (index($0, want) == 1) {
        rest = substr($0, length(want) + 1)
        if (rest == "" || substr(rest, 1, 1) == " ") { inside = 1; found = 1 }
      }
      next
    }
    if (is_heading($0) || is_linkdef($0)) { done = 1; next }
    if ($0 ~ /^[ \t]*$/) { if (printed) blanks++; next }
    while (blanks > 0) { print ""; blanks-- }
    print
    printed = 1
  }
  END {
    if (!found) { print "changelog-section: no \"" want "\" heading" > "/dev/stderr"; exit 1 }
    if (!printed) { print "changelog-section: the \"" want "\" section is empty" > "/dev/stderr"; exit 1 }
  }
' "$file"
