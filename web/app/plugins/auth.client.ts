// Restores session claims from localStorage on page load — useState resets
// on every navigation/reload, but the token/claims persist client-side.
export default defineNuxtPlugin(() => {
  useAuth().restoreClaims()
})
