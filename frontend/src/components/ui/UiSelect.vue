<script setup lang="ts">
import { Check, ChevronDown } from '@lucide/vue'
import {
  SelectContent,
  SelectIcon,
  SelectItem,
  SelectItemIndicator,
  SelectItemText,
  SelectPortal,
  SelectRoot,
  SelectTrigger,
  SelectValue,
  SelectViewport,
} from 'reka-ui'

defineProps<{
  modelValue: string
  options: { label: string; value: string; disabled?: boolean }[]
  placeholder?: string
  ariaLabel?: string
}>()

const emit = defineEmits<{ 'update:modelValue': [value: string] }>()
</script>

<template>
  <SelectRoot :model-value="modelValue" @update:model-value="emit('update:modelValue', String($event))">
    <SelectTrigger class="ui-select-trigger" :aria-label="ariaLabel">
      <SelectValue :placeholder="placeholder" />
      <SelectIcon class="ui-select-icon"><ChevronDown :size="16" /></SelectIcon>
    </SelectTrigger>
    <SelectPortal>
      <SelectContent class="ui-select-content" position="popper" :side-offset="6">
        <SelectViewport class="ui-select-viewport">
          <SelectItem v-for="option in options" :key="option.value" class="ui-select-item" :value="option.value" :disabled="option.disabled">
            <SelectItemText>{{ option.label }}</SelectItemText>
            <SelectItemIndicator class="ui-select-indicator"><Check :size="15" /></SelectItemIndicator>
          </SelectItem>
        </SelectViewport>
      </SelectContent>
    </SelectPortal>
  </SelectRoot>
</template>
