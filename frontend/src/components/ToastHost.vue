<script setup lang="ts">
import { CheckCircle2, CircleAlert, Info } from 'lucide-vue-next'
import { ToastDescription, ToastProvider, ToastRoot, ToastViewport } from 'reka-ui'
import { dismissToast, toasts } from '../toast'
</script>
<template>
  <ToastProvider :duration="3200" swipe-direction="right">
    <ToastRoot v-for="item in toasts" :key="item.id" :open="true" class="toast" :class="item.kind" type="foreground" @update:open="!$event && dismissToast(item.id)">
        <CheckCircle2 v-if="item.kind === 'success'" :size="19" />
        <CircleAlert v-else-if="item.kind === 'error'" :size="19" />
        <Info v-else :size="19" />
        <ToastDescription as-child><span>{{ item.message }}</span></ToastDescription>
    </ToastRoot>
    <ToastViewport class="toast-host" />
  </ToastProvider>
</template>
