# Terminal walkthrough for the README. Regenerate with tools/media.sh.
Output docs/media/demo.gif

Set Shell "bash"
Set FontFamily "Menlo"
Set FontSize 15
Set Width 1050
Set Height 820
Set Padding 16
Set Theme "Catppuccin Mocha"
Set TypingSpeed 45ms
Set PlaybackSpeed 1

Hide
Type "export PS1='$ ' && cd $(dirname $EXPORT_XML) && clear" Enter
Show

Type "# Import your Apple Health export (a zip or the export.xml inside it)" Enter
Sleep 800ms
Type "healthcli import apple export.xml" Enter
Sleep 3s
Hide
Type "cp $SHIFTED_DB $HEALTHCLI_DB && clear" Enter
Show

Type "# One screen: every metric against your own 28-day usual" Enter
Type "healthcli overview --category vitals,sleep --weeks 0" Enter
Sleep 5s
Hide
Type "clear" Enter
Show

Type "# A metric over time" Enter
Type "healthcli trend steps --days 14" Enter
Sleep 3s
Type "healthcli trend sleep --days 14" Enter
Sleep 3s
Hide
Type "clear" Enter
Show

Type "# Calendar heatmap" Enter
Type "healthcli heatmap steps --weeks 12" Enter
Sleep 4s
Hide
Type "clear" Enter
Show

Type "# And plain SQL for everything else" Enter
Type `healthcli query "SELECT metric, round(avg(value),1) AS avg FROM samples WHERE metric IN ('resting_heart_rate','hrv_sdnn') GROUP BY metric"` Enter
Sleep 4s
