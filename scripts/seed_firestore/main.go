// scripts/seed_firestore/main.go
//
// Heavy Firestore seeder for the gcp-local emulator. Cloud Firestore is
// gRPC-only, so we use the real `cloud.google.com/go/firestore` client
// against the emulator via FIRESTORE_EMULATOR_HOST. Invoked from
// scripts/seed.sh; safe to run on its own as well.
//
//	go run ./scripts/seed_firestore --addr localhost:4443
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"cloud.google.com/go/firestore"
)

func main() {
	addr := flag.String("addr", "localhost:4443", "emulator gRPC host:port (e.g. localhost:4443)")
	project := flag.String("project", "local-project", "project id")
	quiet := flag.Bool("quiet", false, "suppress progress output")
	flag.Parse()

	_ = os.Setenv("FIRESTORE_EMULATOR_HOST", *addr)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := firestore.NewClient(ctx, *project)
	if err != nil {
		log.Fatalf("firestore client: %v", err)
	}
	defer func() { _ = client.Close() }()

	logf := func(format string, args ...any) {
		if !*quiet {
			fmt.Fprintf(os.Stderr, format+"\n", args...)
		}
	}

	now := time.Now().UTC()

	// users — flat documents with addresses array, mixed types.
	users := []struct {
		ID   string
		Data map[string]any
	}{
		{"u_1001", map[string]any{
			"name": "Alice Tan", "email": "alice@example.com", "age": int64(30),
			"role": "admin", "active": true, "createdAt": now.Add(-90 * 24 * time.Hour),
			"addresses": []any{
				map[string]any{"label": "home", "city": "London", "country": "UK"},
			},
			"tags": []any{"power-user", "early-access"},
		}},
		{"u_1002", map[string]any{
			"name": "Bob Mendes", "email": "bob@example.com", "age": int64(27),
			"role": "user", "active": true, "createdAt": now.Add(-60 * 24 * time.Hour),
			"addresses": []any{
				map[string]any{"label": "home", "city": "Lisbon", "country": "PT"},
				map[string]any{"label": "office", "city": "Lisbon", "country": "PT"},
			},
			"tags": []any{"newsletter"},
		}},
		{"u_1003", map[string]any{
			"name": "Carla Iwu", "email": "carla@example.com", "age": int64(34),
			"role": "user", "active": false, "createdAt": now.Add(-180 * 24 * time.Hour),
			"tags": []any{},
		}},
		{"u_1004", map[string]any{
			"name": "Dmitri Volkov", "email": "dmitri@example.com", "age": int64(41),
			"role": "support", "active": true, "createdAt": now.Add(-30 * 24 * time.Hour),
			"addresses": []any{
				map[string]any{"label": "home", "city": "Berlin", "country": "DE"},
			},
		}},
		{"u_1005", map[string]any{
			"name": "Esther Quinn", "email": "esther@example.com", "age": int64(29),
			"role": "user", "active": true, "createdAt": now.Add(-12 * 24 * time.Hour),
			"tags": []any{"beta"},
		}},
		{"u_1006", map[string]any{
			"name": "Farouk Saleh", "email": "farouk@example.com", "age": int64(38),
			"role": "user", "active": true, "createdAt": now.Add(-5 * 24 * time.Hour),
		}},
		{"u_1007", map[string]any{
			"name": "Greta Holm", "email": "greta@example.com", "age": int64(26),
			"role": "user", "active": true, "createdAt": now.Add(-1 * 24 * time.Hour),
		}},
		{"u_1008", map[string]any{
			"name": "Hiroshi Tanaka", "email": "hiroshi@example.com", "age": int64(45),
			"role": "admin", "active": true, "createdAt": now.Add(-365 * 24 * time.Hour),
		}},
	}
	for _, u := range users {
		if _, err := client.Collection("users").Doc(u.ID).Set(ctx, u.Data); err != nil {
			log.Printf("user %s: %v", u.ID, err)
		}
	}
	logf("  ✓ users: %d documents", len(users))

	// products — simple inventory.
	products := []struct {
		ID   string
		Data map[string]any
	}{
		{"p_001", map[string]any{"name": "Standard Widget", "sku": "STD-001", "price": 12.50, "currency": "USD", "inStock": int64(120), "tags": []any{"widget", "standard"}}},
		{"p_002", map[string]any{"name": "Premium Widget", "sku": "PRM-002", "price": 29.95, "currency": "USD", "inStock": int64(42), "tags": []any{"widget", "premium"}}},
		{"p_003", map[string]any{"name": "Deluxe Sprocket", "sku": "DLX-003", "price": 89.00, "currency": "USD", "inStock": int64(8), "tags": []any{"sprocket", "deluxe"}}},
		{"p_004", map[string]any{"name": "Hex Bolt 50pk", "sku": "HEX-004", "price": 5.49, "currency": "USD", "inStock": int64(2400), "tags": []any{"fastener"}}},
		{"p_005", map[string]any{"name": "Annual Subscription", "sku": "SUB-ANN", "price": 120.00, "currency": "USD", "inStock": int64(0), "tags": []any{"subscription"}, "recurring": true}},
		{"p_006", map[string]any{"name": "Monthly Subscription", "sku": "SUB-MON", "price": 12.00, "currency": "USD", "inStock": int64(0), "tags": []any{"subscription"}, "recurring": true}},
		{"p_007", map[string]any{"name": "Replacement Belt", "sku": "RPL-007", "price": 18.75, "currency": "USD", "inStock": int64(54), "tags": []any{"replacement", "belt"}}},
		{"p_008", map[string]any{"name": "Carbon Wheel", "sku": "CRB-008", "price": 489.00, "currency": "USD", "inStock": int64(3), "tags": []any{"wheel", "carbon", "premium"}}},
	}
	for _, p := range products {
		if _, err := client.Collection("products").Doc(p.ID).Set(ctx, p.Data); err != nil {
			log.Printf("product %s: %v", p.ID, err)
		}
	}
	logf("  ✓ products: %d documents", len(products))

	// orders — references to user + products + status field for query demos.
	statuses := []string{"pending", "paid", "shipped", "delivered", "cancelled", "refunded"}
	orders := []struct {
		ID   string
		Data map[string]any
	}{
		{"o_2001", orderDoc("u_1001", 42.10, "paid", now.Add(-3*time.Hour), []string{"p_001", "p_002"})},
		{"o_2002", orderDoc("u_1002", 128.00, "shipped", now.Add(-2*24*time.Hour), []string{"p_003"})},
		{"o_2003", orderDoc("u_1004", 8.49, "delivered", now.Add(-7*24*time.Hour), []string{"p_004"})},
		{"o_2004", orderDoc("u_1005", 120.00, "paid", now.Add(-1*24*time.Hour), []string{"p_005"})},
		{"o_2005", orderDoc("u_1006", 35.50, "pending", now, []string{"p_001", "p_007"})},
		{"o_2006", orderDoc("u_1001", 489.00, "cancelled", now.Add(-12*time.Hour), []string{"p_008"})},
		{"o_2007", orderDoc("u_1007", 12.00, "paid", now.Add(-30*time.Minute), []string{"p_006"})},
		{"o_2008", orderDoc("u_1002", 24.00, "refunded", now.Add(-15*24*time.Hour), []string{"p_006", "p_006"})},
		{"o_2009", orderDoc("u_1008", 95.00, "delivered", now.Add(-5*24*time.Hour), []string{"p_002", "p_002", "p_001"})},
		{"o_2010", orderDoc("u_1005", 18.75, "shipped", now.Add(-4*time.Hour), []string{"p_007"})},
	}
	_ = statuses
	for _, o := range orders {
		if _, err := client.Collection("orders").Doc(o.ID).Set(ctx, o.Data); err != nil {
			log.Printf("order %s: %v", o.ID, err)
		}
	}
	logf("  ✓ orders: %d documents", len(orders))

	// sessions — short-lived, lots of them.
	for i := 0; i < 12; i++ {
		id := fmt.Sprintf("s_%04d", 8000+i)
		userID := users[i%len(users)].ID
		data := map[string]any{
			"userId":    userID,
			"ip":        fmt.Sprintf("10.0.%d.%d", i, (i*17)%256),
			"userAgent": "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 gcp-local-seed",
			"startedAt": now.Add(-time.Duration(i*7) * time.Minute),
			"active":    i%3 != 0,
		}
		if _, err := client.Collection("sessions").Doc(id).Set(ctx, data); err != nil {
			log.Printf("session %s: %v", id, err)
		}
	}
	logf("  ✓ sessions: 12 documents")

	// audit_logs — append-only style, many small docs.
	actions := []string{"user.login", "user.logout", "order.create", "order.cancel", "product.update", "secret.access", "key.rotate"}
	for i := 0; i < 30; i++ {
		id := fmt.Sprintf("a_%05d", 1+i)
		action := actions[i%len(actions)]
		actor := users[i%len(users)].ID
		data := map[string]any{
			"action":    action,
			"actor":     actor,
			"target":    fmt.Sprintf("res_%d", 5000+i),
			"timestamp": now.Add(-time.Duration(i*11) * time.Minute),
			"ip":        fmt.Sprintf("10.0.%d.%d", i%32, (i*23)%256),
			"meta": map[string]any{
				"requestId": fmt.Sprintf("req_%d", 9000+i),
				"ua":        "curl/8.4.0",
			},
		}
		if _, err := client.Collection("audit_logs").Doc(id).Set(ctx, data); err != nil {
			log.Printf("audit_log %s: %v", id, err)
		}
	}
	logf("  ✓ audit_logs: 30 documents")

	// nested subcollection example: users/u_1001/posts/p_001
	post := map[string]any{
		"title":     "Welcome to gcp-local",
		"body":      "This is a doc inside a subcollection. The console shows it under users/u_1001/posts.",
		"published": true,
		"tags":      []any{"intro", "docs"},
		"createdAt": now.Add(-2 * 24 * time.Hour),
	}
	if _, err := client.Collection("users").Doc("u_1001").Collection("posts").Doc("post_001").Set(ctx, post); err != nil {
		log.Printf("subcollection post: %v", err)
	}
	logf("  ✓ users/u_1001/posts: 1 subcollection document")

	logf("  total: ~%d Firestore documents", len(users)+len(products)+len(orders)+12+30+1)
}

func orderDoc(userID string, total float64, status string, createdAt time.Time, productIDs []string) map[string]any {
	items := make([]any, 0, len(productIDs))
	for _, pid := range productIDs {
		items = append(items, map[string]any{"productId": pid, "qty": int64(1)})
	}
	return map[string]any{
		"userId":    userID,
		"total":     total,
		"currency":  "USD",
		"status":    status,
		"items":     items,
		"createdAt": createdAt,
	}
}
