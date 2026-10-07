// The firm login and a staff member each change their OWN password on their own
// route (platform-api: PUT /account/password is the firm's, PUT /staff/me/password
// is the signed-in staff member's; the firm route refuses a staff session).
export function passwordPath(isStaff: boolean): string {
  return isStaff ? '/staff/me/password' : '/account/password'
}
