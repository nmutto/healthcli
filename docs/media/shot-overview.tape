# Screenshot for the README. Regenerate with tools/media.sh.
Set Shell "bash"
Set FontFamily "Menlo"
Set FontSize 15
Set Width 1050
Set Height 1680
Set Padding 16
Set Theme "Catppuccin Mocha"

Hide
Type "export PS1='$ ' && cp $SHIFTED_DB $HEALTHCLI_DB && clear" Enter
Show

Type "healthcli overview --weeks 8" Enter
Sleep 1500ms
Screenshot docs/media/overview.png
