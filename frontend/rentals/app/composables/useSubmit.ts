import { ApiError } from '~/utils/apiError'

// Wraps a form submit: loading flag, per-field errors from a 422 object, and
// one general message for everything else (rule R5).
export function useSubmit() {
  const loading = ref(false)
  const fields = ref<Record<string, string>>({})
  const message = ref('')
  const status = ref(0)

  async function run<T>(fn: () => Promise<T>): Promise<T | undefined> {
    loading.value = true
    fields.value = {}
    message.value = ''
    status.value = 0
    try {
      return await fn()
    } catch (e) {
      if (e instanceof ApiError) {
        status.value = e.status
        // A 422 with field keys goes under the fields; a lone message is general.
        if (e.isValidation && Object.keys(e.fields).length) fields.value = e.fields
        else message.value = e.message
      } else {
        message.value = 'Something went wrong. Please try again.'
      }
      return undefined
    } finally {
      loading.value = false
    }
  }

  return { loading, fields, message, status, run }
}
