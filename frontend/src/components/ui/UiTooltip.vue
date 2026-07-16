<script setup lang="ts">
import { TooltipContent, TooltipPortal, TooltipRoot, TooltipTrigger } from 'reka-ui'

withDefaults(defineProps<{ text: string; side?: 'top' | 'right' | 'bottom' | 'left' }>(), { side: 'bottom' })
</script>

<template>
  <TooltipRoot>
    <TooltipTrigger as-child><slot /></TooltipTrigger>
    <TooltipPortal><TooltipContent class="ui-tooltip" :side="side" :side-offset="7">{{ text }}</TooltipContent></TooltipPortal>
  </TooltipRoot>
</template>
