// OOP entities: unexported fields + constructor functions
// (`NewX(...) (*X, error)`) validating invariants at construction; each entity
// carries the METHODS that only need its own fields — anything needing another
// package's state is a service method instead (internal/money, internal/billing,
// etc.), never a free function reaching into these structs from outside.
package domain
