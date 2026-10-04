%host shop
gluon -host . -e ':layout pricing.LineItem'
gluon -host . -e 'pricing.Total(pricing.Sample()).String()'
