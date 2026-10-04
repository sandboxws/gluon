gluon -e 'strings.ToUpper("hi")'
gluon -e 'x := 21' -e 'x * 2'
echo 'strings.Repeat("ab", 3)' | gluon
gluon -e '[]int{1, 2, 3}' -json
gluon -e ':layout struct{ a bool; b int64; c bool }' -json
gluon -e 'strings.Repeat("ab")'; echo "exit $?"
