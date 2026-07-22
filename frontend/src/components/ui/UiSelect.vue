<script setup lang="ts">
import { ChevronDown } from '@lucide/vue'

defineProps<{
  modelValue: string
  options: { label: string; value: string; disabled?: boolean }[]
  placeholder?: string
  ariaLabel?: string
}>()

const emit = defineEmits<{ 'update:modelValue': [value: string] }>()

function updateValue(event: Event) {
  emit('update:modelValue', (event.target as HTMLSelectElement).value)
}
</script>

<template>
  <span class="ui-select-native">
    <select class="ui-select-trigger" :value="modelValue" :aria-label="ariaLabel" @change="updateValue">
      <option v-if="placeholder" value="" disabled>{{ placeholder }}</option>
      <option v-for="option in options" :key="option.value" :value="option.value" :disabled="option.disabled">{{ option.label }}</option>
    </select>
    <ChevronDown class="ui-select-icon" :size="16" aria-hidden="true" />
  </span>
</template>
