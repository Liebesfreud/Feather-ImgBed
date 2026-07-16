<script setup lang="ts">
import { AlertTriangle } from 'lucide-vue-next'
import {
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogOverlay,
  AlertDialogPortal,
  AlertDialogRoot,
  AlertDialogTitle,
} from 'reka-ui'

withDefaults(defineProps<{
  open: boolean
  title: string
  description: string
  confirmLabel?: string
  busy?: boolean
}>(), { confirmLabel: '确认', busy: false })

const emit = defineEmits<{ 'update:open': [value: boolean]; confirm: [] }>()
</script>

<template>
  <AlertDialogRoot :open="open" @update:open="emit('update:open', $event)">
    <AlertDialogPortal>
      <AlertDialogOverlay class="dialog-overlay" />
      <AlertDialogContent class="confirm-dialog">
        <span class="confirm-dialog-icon"><AlertTriangle :size="22" /></span>
        <div>
          <AlertDialogTitle class="confirm-dialog-title">{{ title }}</AlertDialogTitle>
          <AlertDialogDescription class="confirm-dialog-description">{{ description }}</AlertDialogDescription>
        </div>
        <div class="confirm-dialog-actions">
          <AlertDialogCancel class="soft-button" :disabled="busy">取消</AlertDialogCancel>
          <AlertDialogAction class="primary-button danger-action" :disabled="busy" @click="emit('confirm')">{{ busy ? '处理中…' : confirmLabel }}</AlertDialogAction>
        </div>
      </AlertDialogContent>
    </AlertDialogPortal>
  </AlertDialogRoot>
</template>
