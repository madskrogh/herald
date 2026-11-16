# When to Mock

Mock at **system boundaries** only:

- External APIs (payment, email, etc.)
- Time/randomness
- File system (sometimes)

Don't mock:

- Postgres or NATS: tests run against the real ones (see `.claude/rules/go-testing.md`)
- Your own classes/modules
- Internal collaborators
- Anything you control

## Designing for Mockability

At system boundaries, design interfaces that are easy to mock:

**1. Use dependency injection**

Pass external dependencies in rather than creating them internally:

```go
// Easy to replace in a test
func ProcessPayment(ctx context.Context, o Order, c Charger) error {
	return c.Charge(ctx, o.Total)
}

// Hard to replace
func ProcessPayment(ctx context.Context, o Order) error {
	c := stripe.NewClient(os.Getenv("STRIPE_KEY"))
	return c.Charge(ctx, o.Total)
}
```

**2. Prefer specific operations over a generic fetcher**

Define one method per external operation instead of one generic method with conditional logic:

```go
// GOOD: each method returns one specific shape
type UserAPI interface {
	GetUser(ctx context.Context, id string) (User, error)
	GetOrders(ctx context.Context, userID string) ([]Order, error)
}

// BAD: a fake needs conditional logic on the path
type API interface {
	Do(ctx context.Context, method, path string, body []byte) ([]byte, error)
}
```

The specific approach means:
- Each fake returns one specific shape
- No conditional logic in test setup
- Easier to see which endpoints a test exercises
- Type safety per endpoint
