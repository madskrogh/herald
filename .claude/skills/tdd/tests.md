# Good and Bad Tests

The examples are illustrative: the names are not Herald's API.

## Good Tests

**Integration-style**: Test through real interfaces, not mocks of internal parts.

```go
// GOOD: tests observable behaviour
func TestCheckout_ValidCart(t *testing.T) {
	cart := NewCart()
	cart.Add(product)
	got, err := Checkout(ctx, cart, payment)
	if err != nil {
		t.Fatalf("Checkout: %v", err)
	}
	if got.Status != StatusConfirmed {
		t.Errorf("Status = %v, want %v", got.Status, StatusConfirmed)
	}
}
```

Characteristics:

- Tests behavior users/callers care about
- Uses public API only
- Survives internal refactors
- Describes WHAT, not HOW
- One logical assertion per test

## Bad Tests

**Implementation-detail tests**: Coupled to internal structure.

```go
// BAD: tests implementation details
func TestCheckout_CallsProcess(t *testing.T) {
	pay := &recordingPayment{}
	Checkout(ctx, cart, pay)
	if pay.calls != 1 || pay.amount != cart.Total() {
		t.Errorf("Process called %d times with %v", pay.calls, pay.amount)
	}
}
```

Red flags:

- Mocking internal collaborators
- Testing private methods
- Asserting on call counts/order
- Test breaks when refactoring without behavior change
- Test name describes HOW not WHAT
- Verifying through external means instead of interface

```go
// BAD: bypasses the interface to verify
func TestCreateUser_InsertsRow(t *testing.T) {
	CreateUser(ctx, store, "Alice")
	var n int
	db.QueryRow(ctx, "SELECT count(*) FROM users WHERE name = $1", "Alice").Scan(&n)
	if n != 1 {
		t.Errorf("rows = %d, want 1", n)
	}
}

// GOOD: verifies through the interface
func TestCreateUser_Retrievable(t *testing.T) {
	u, err := CreateUser(ctx, store, "Alice")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	got, err := GetUser(ctx, store, u.ID)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if got.Name != "Alice" {
		t.Errorf("Name = %q, want %q", got.Name, "Alice")
	}
}
```

**Tautological tests**: Expected value restates the implementation, so the test passes by construction.

```go
// BAD: expected value is recomputed the way the code computes it
items := []Item{{Price: 10}, {Price: 5}}
want := 0
for _, it := range items {
	want += it.Price
}
if got := Total(items); got != want { ... }

// GOOD: expected value is an independent, known literal
if got := Total([]Item{{Price: 10}, {Price: 5}}); got != 15 {
	t.Errorf("Total = %d, want 15", got)
}
```
