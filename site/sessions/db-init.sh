%host shop
gluon init -no-prompt
gluon init -no-prompt -write -local
cat gluon.toml
gluon -host . -e ':db'
%clip 18
gluon -host . -e ':query -json SELECT id, email, plan FROM users WHERE id < 3' -json
