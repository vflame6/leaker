# Sourced by capture.zsh in an interactive zsh: loads the completion under
# test from $LEAKER_ZSH_FPATH and replaces compadd with a function that prints
# every candidate instead of showing it. Based on Valodim/zsh-capture-completion.
PROMPT=
fpath=("$LEAKER_ZSH_FPATH" $fpath)
autoload -Uz compinit
compinit -u -D
bindkey '^I' complete-word
null-line() { echo -E - $'\0' }
compprefuncs=(null-line)
comppostfuncs=(null-line exit)
zstyle ':completion:*' list-grouped false
zstyle ':completion:*' insert-tab false
zstyle ':completion:*' list-separator ''
zmodload zsh/zutil
compadd() {
  # Calls that only collect matches (-O, -A, -D) show nothing.
  if [[ ${@[1,(i)(-|--)]} == *-(O|A|D)\ * ]]; then
    builtin compadd "$@"
    return $?
  fi
  typeset -a __hits __dscr __tmp
  if (( $@[(I)-d] )); then
    __tmp=${@[$[${@[(i)-d]}+1]]}
    if [[ $__tmp == \(* ]]; then
      eval "__dscr=$__tmp"
    else
      __dscr=("${(@P)__tmp}")
    fi
  fi
  builtin compadd -A __hits -D __dscr "$@"
  setopt localoptions norcexpandparam extendedglob
  typeset -A apre hpre hsuf asuf
  zparseopts -E P:=apre p:=hpre S:=asuf s:=hsuf
  [[ -n $__hits ]] || return
  local i
  for i in {1..$#__hits}; do
    echo -E - $IPREFIX$apre$hpre$__hits[$i]$hsuf$asuf
  done
}
echo ok
