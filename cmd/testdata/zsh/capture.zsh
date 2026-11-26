# zsh -f capture.zsh INIT LINE prints the completions zsh offers for LINE,
# one per line. INIT is init.zsh; the completion under test is read from
# $LEAKER_ZSH_FPATH.
zmodload zsh/zpty || exit 2
zpty z zsh -f -i
zpty -w z "source ${(q)1}"
local line ready=0
repeat 20; do
  zpty -r z line || break
  if [[ $line == ok* ]]; then
    ready=1
    break
  fi
done
(( ready )) || { echo "zsh did not load $1" >&2; exit 2 }
zpty -w z "$2"$'\t'
integer seen=0
while zpty -r z; do :; done | while IFS= read -r line; do
  # The candidates are printed between two lines ending in a NUL.
  if [[ $line == *$'\0\r' ]]; then
    (( seen++ )) && exit 0 || continue
  fi
  (( seen )) && echo -E - ${line%$'\r'}
done
