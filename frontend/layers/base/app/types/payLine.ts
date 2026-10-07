// One payable line on a public pay page. `type` is the product's own key for
// it (rentals: RENT, WATER, RENT_DEPOSIT, ...); the form only needs it opaque.
export interface PayBalance {
  type: string
  balance: string // what is owed; negative = credit
  // Whether this line can be paid right now. A settled line may still be
  // shown, just not payable.
  payable: boolean
}
