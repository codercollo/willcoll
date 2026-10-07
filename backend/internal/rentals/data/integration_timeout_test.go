package data

import "time"

// integrationTimeout is the per-call budget the Postgres integration tests
// give the models. New connections can take a second or more on a loaded
// development machine, which the 3s production default does not allow for.
const integrationTimeout = 30 * time.Second
