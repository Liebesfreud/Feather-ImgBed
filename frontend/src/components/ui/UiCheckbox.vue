<script setup lang="ts">
import { Check } from 'lucide-vue-next'
import { CheckboxIndicator, CheckboxRoot } from 'reka-ui'

defineProps<{ modelValue: boolean; ariaLabel?: string }>()
const emit = defineEmits<{ 'update:modelValue': [value: boolean] }>()
</script>

<template>
  <CheckboxRoot class="ui-checkbox" :model-value="modelValue" :aria-label="ariaLabel" @update:model-value="emit('update:modelValue', Boolean($event))">
    <CheckboxIndicator class="ui-checkbox-indicator"><Check :size="12" stroke-width="3" /></CheckboxIndicator>
  </CheckboxRoot>
</template>
