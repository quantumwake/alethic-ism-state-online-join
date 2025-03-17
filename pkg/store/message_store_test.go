package store_test

import (
	"fmt"
	"testing"
	"time"
)

func TestMessageStore_AddMessage(t *testing.T) {

	// Example 1: Order data coming from two different systems
	// Data arrives from the order system

	var orders []any
	var ordersDetail []any

	for c := 23000; c < 23005; c++ {
		for i := 0; i < 10; i++ {
			orders = append(orders, map[string]any{
				"order_id":    fmt.Sprintf("ORD-%d", c),
				"customer_id": fmt.Sprintf("CUST-%d", i),
				"source":      "order_system",
				"status":      "pending",
			})

			ordersDetail = append(ordersDetail, map[string]any{
				"order_id":    fmt.Sprintf("ORD-%d", c),
				"customer_id": fmt.Sprintf("CUST-%d", i),
				"amount":      99.99,
				"timestamp":   time.Now().Unix(),
			})
		}
	}

	print(orders)
	print(ordersDetail)

	//joinBlock := store.NewJoinBlock()
	//
	//msg := store.NewMessage("test")
	//ms.AddMessage(msg)
	//
	//if ms.messages["test"] == nil {
	//	t.Errorf("Message not added to store")
	//}
}
