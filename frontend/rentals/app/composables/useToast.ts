export interface ToastItem { id: number; kind: 'success' | 'error' | 'info'; message: string }

export function useToast() {
  const items = useState<ToastItem[]>('toasts', () => [])
  let seq = useState('toast-seq', () => 0)

  function push(kind: ToastItem['kind'], message: string, ms = 5000) {
    const id = ++seq.value
    items.value = [...items.value, { id, kind, message }]
    if (import.meta.client) setTimeout(() => dismiss(id), ms)
  }
  function dismiss(id: number) { items.value = items.value.filter(t => t.id !== id) }

  return {
    items,
    dismiss,
    success: (m: string) => push('success', m),
    info: (m: string) => push('info', m),
    error: (m: string) => push('error', m, 8000),
    /** Show any thrown value (ApiError or otherwise) as an error toast. */
    fail: (e: unknown) => push('error', e instanceof Error ? e.message : 'Something went wrong.', 8000),
  }
}
