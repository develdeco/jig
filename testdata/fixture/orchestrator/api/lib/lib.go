// Package lib is a workspace the api and the web repos both declare, so a
// slice that names it bare must name its repo.
package lib

// Cents is dollars in cents.
func Cents(dollars int) int { return dollars * 100 }
