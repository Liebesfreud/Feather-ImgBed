<script setup lang="ts">
import { SwitchRoot, SwitchThumb } from 'reka-ui'

defineProps<{ modelValue: boolean; ariaLabel?: string; disabled?: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [value: boolean] }>()
</script>

<template>
  <SwitchRoot class="ui-switch" :model-value="modelValue" :aria-label="ariaLabel" :disabled="disabled" @update:model-value="emit('update:modelValue', $event)">
    <SwitchThumb class="ui-switch-thumb" />
  </SwitchRoot>
</template>
