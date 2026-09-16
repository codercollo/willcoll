export function useAuth() {
  const token = useState<string | null>('auth-token', () => {
    if (import.meta.client) {
      return localStorage.getItem('auth-token')
    }
    return null
  })

  async function login(email: string, password: string) {
    const response = await $fetch<{ data: { token: string } }>('/v1/auth/login', {
      method: 'POST',
      body: { email, password },
    })
    token.value = response.data.token
    if (import.meta.client) {
      localStorage.setItem('auth-token', response.data.token)
    }
    return response.data.token
  }

  async function activate(activationToken: string, password: string) {
    await $fetch('/v1/users/activate', {
      method: 'PUT',
      body: { token: activationToken, password },
    })
  }

  async function resetPassword(resetToken: string, newPassword: string) {
    await $fetch('/v1/users/password', {
      method: 'PUT',
      body: { token: resetToken, new_password: newPassword },
    })
  }

  function logout() {
    token.value = null
    if (import.meta.client) {
      localStorage.removeItem('auth-token')
    }
  }

  return { token, login, activate, resetPassword, logout }
}
