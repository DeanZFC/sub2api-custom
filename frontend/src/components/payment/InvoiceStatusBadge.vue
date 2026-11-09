<template>
  <span class="inline-flex rounded-full px-2.5 py-1 text-xs font-medium" :class="classes[status]">
    {{ t(`invoice.${status}`) }}
  </span>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { InvoiceStatus } from '@/types/invoice'

defineProps<{ status: InvoiceStatus }>()
const { t } = useI18n()
const classes: Record<InvoiceStatus, string> = {
  pending: 'bg-amber-50 text-amber-700 dark:bg-amber-900/20 dark:text-amber-300',
  issued: 'bg-emerald-50 text-emerald-700 dark:bg-emerald-900/20 dark:text-emerald-300',
  rejected: 'bg-red-50 text-red-700 dark:bg-red-900/20 dark:text-red-300',
}
</script>
