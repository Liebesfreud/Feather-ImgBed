import { reactive } from 'vue'

export type ToastKind = 'success' | 'error' | 'info'
export interface Toast { id: number; message: string; kind: ToastKind }
export const toasts = reactive<Toast[]>([])
let nextId = 1

export function toast(message: string, kind: ToastKind = 'success') {
  const item = { id: nextId++, message, kind }
  toasts.push(item)
}

export function dismissToast(id: number) {
  const index = toasts.findIndex((value) => value.id === id)
  if (index >= 0) toasts.splice(index, 1)
}
