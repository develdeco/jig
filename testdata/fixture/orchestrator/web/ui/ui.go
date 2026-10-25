// Package ui is the web repo's product workspace.
package ui

import "fmt"

// Label shows an order total, given in cents, as dollars.
func Label(cents int) string {
	return fmt.Sprintf("$%d.%02d", cents/100, cents%100)
}
