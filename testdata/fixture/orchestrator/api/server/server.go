// Package server is the api repo's product workspace.
package server

// Total adds up the prices of an order, in cents.
func Total(prices ...int) int {
	sum := 0
	for _, p := range prices {
		sum += p
	}
	return sum
}
