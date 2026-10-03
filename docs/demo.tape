# Regenerate the README demo with: vhs docs/demo.tape
Output docs/demo.gif

Set Shell "bash"
Set FontSize 14
Set Width 1400
Set Height 800
Set Theme "Catppuccin Mocha"
Set TypingSpeed 80ms

Hide
Type "make build >/dev/null && export PATH=$PWD/bin:$PATH && clear"
Enter
Show

Type "vanityrig"
Sleep 400ms
Enter
Sleep 1.5s

# Edit the word field and type a word
Enter
Type "cafe"
Enter
Sleep 2s

# Start the search and let it find matches
Type "s"
Sleep 8s

Type "q"
Sleep 500ms
