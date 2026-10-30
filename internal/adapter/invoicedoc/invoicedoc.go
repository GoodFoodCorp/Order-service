// Package invoicedoc renders an invoice: as a page (download, print to PDF)
// and as an email (HTML and plain text).
package invoicedoc

import (
	"bytes"
	"fmt"
	"html/template"
	"strings"
	"time"
	// Invoices show Paris time, whatever the image ships.
	_ "time/tzdata"

	"goodfood/order-service/internal/domain"
)

// Euros formats cents the French way: 1 234,50 €.
func Euros(cents int64) string {
	sign := ""
	if cents < 0 {
		sign, cents = "-", -cents
	}
	whole := fmt.Sprintf("%d", cents/100)
	var b strings.Builder
	for i, r := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteRune(' ')
		}
		b.WriteRune(r)
	}
	return fmt.Sprintf("%s%s,%02d €", sign, b.String(), cents%100)
}

var paris = func() *time.Location {
	if loc, err := time.LoadLocation("Europe/Paris"); err == nil {
		return loc
	}
	return time.UTC
}()

func date(t time.Time) string { return t.In(paris).Format("02/01/2006 à 15:04") }

var page = template.Must(template.New("invoice").Funcs(template.FuncMap{
	"euros": Euros, "date": date, "short": func(id string) string { return id[:min(8, len(id))] },
	"pickup": func(f domain.Fulfillment) bool { return f == domain.FulfillmentPickup },
}).Parse(`<!doctype html>
<html lang="fr"><head><meta charset="utf-8"><title>Facture {{.Number}}</title>
<meta name="viewport" content="width=device-width, initial-scale=1">
<style>
 body{font-family:'Open Sans',Arial,sans-serif;color:#1c2321;margin:0;background:#f5f7f6}
 .sheet{max-width:720px;margin:24px auto;background:#fff;padding:32px;border-radius:12px}
 h1{font-family:Montserrat,Arial,sans-serif;color:#004430;margin:0;font-size:24px}
 .muted{color:#6b7280;font-size:13px} .row{display:flex;justify-content:space-between;gap:24px;flex-wrap:wrap}
 table{width:100%;border-collapse:collapse;margin-top:24px;font-size:14px}
 th{text-align:left;color:#6b7280;font-weight:600;border-bottom:1px solid #e5e7eb;padding:8px 4px}
 td{border-bottom:1px solid #f0f0f0;padding:8px 4px} .num{text-align:right;white-space:nowrap}
 .total td{font-weight:700;color:#004430;font-size:16px;border:0}
 .print{margin-top:24px;text-align:center} @media print{body{background:#fff}.sheet{margin:0;padding:0}.print{display:none}}
</style></head><body><div class="sheet">
<div class="row">
 <div><h1>Facture</h1><p class="muted">N° <strong>{{.Number}}</strong><br>Émise le {{date .IssuedAt}}<br>Commande #{{short .OrderID}}</p></div>
 <div class="muted" style="text-align:right"><strong style="color:#004430">{{.Seller.Name}}</strong><br>{{.Seller.Address}}
 {{if .Seller.SIRET}}<br>SIRET {{.Seller.SIRET}}{{end}}{{if .Seller.VATNumber}}<br>TVA {{.Seller.VATNumber}}{{end}}</div>
</div>
<div class="row" style="margin-top:16px">
 <div class="muted"><strong>Restaurant</strong><br>{{.RestaurantName}}<br>{{.RestaurantAddress}}</div>
 <div class="muted" style="text-align:right"><strong>Client</strong><br>{{.CustomerEmail}}<br>{{if pickup .Fulfillment}}À emporter{{else}}{{.DeliveryAddress}}{{end}}</div>
</div>
<table><thead><tr><th>Désignation</th><th class="num">Qté</th><th class="num">Prix unit. TTC</th><th class="num">TVA</th><th class="num">Total TTC</th></tr></thead><tbody>
{{range .Lines}}<tr><td>{{.Label}}</td><td class="num">{{.Quantity}}</td><td class="num">{{euros .UnitPriceCents}}</td><td class="num">{{.VATRate}} %</td><td class="num">{{euros .TotalCents}}</td></tr>{{end}}
{{if .DiscountCents}}<tr><td>Réduction{{if .PromoCode}} ({{.PromoCode}}){{end}}</td><td></td><td></td><td class="num">10 %</td><td class="num">−{{euros .DiscountCents}}</td></tr>{{end}}
{{if .TipCents}}<tr><td>Pourboire (non soumis à la TVA)</td><td></td><td></td><td></td><td class="num">{{euros .TipCents}}</td></tr>{{end}}
</tbody></table>
<table><tbody>
{{range .VAT}}<tr><td class="muted">TVA {{.Rate}} % sur {{euros .BaseCents}} HT</td><td class="num">{{euros .VATCents}}</td></tr>{{end}}
<tr><td class="muted">Total HT</td><td class="num">{{euros .TotalExclVATCents}}</td></tr>
<tr><td class="muted">Total TVA</td><td class="num">{{euros .TotalVATCents}}</td></tr>
<tr class="total"><td>Total payé TTC</td><td class="num">{{euros .TotalCents}}</td></tr>
</tbody></table>
<p class="muted" style="margin-top:24px">Payée par carte bancaire. Merci pour votre commande !</p>
<p class="print"><button onclick="window.print()" style="padding:10px 18px;border:0;border-radius:10px;background:#004430;color:#fff;font-weight:600;cursor:pointer">Imprimer / Enregistrer en PDF</button></p>
</div></body></html>`))

// HTML is the invoice as a page.
func HTML(inv *domain.Invoice) ([]byte, error) {
	var b bytes.Buffer
	if err := page.Execute(&b, inv); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// Text is the invoice for mail clients without HTML.
func Text(inv *domain.Invoice) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Facture %s — %s\nCommande #%s, %s\n\n", inv.Number, inv.Seller.Name, inv.OrderID[:min(8, len(inv.OrderID))], date(inv.IssuedAt))
	for _, l := range inv.Lines {
		fmt.Fprintf(&b, "%d × %s : %s (TVA %d %%)\n", l.Quantity, l.Label, Euros(l.TotalCents), l.VATRate)
	}
	if inv.DiscountCents > 0 {
		fmt.Fprintf(&b, "Réduction : -%s\n", Euros(inv.DiscountCents))
	}
	if inv.TipCents > 0 {
		fmt.Fprintf(&b, "Pourboire : %s\n", Euros(inv.TipCents))
	}
	fmt.Fprintf(&b, "\nTotal HT : %s\nTotal TVA : %s\nTotal payé TTC : %s\n", Euros(inv.TotalExclVATCents), Euros(inv.TotalVATCents), Euros(inv.TotalCents))
	return b.String()
}
