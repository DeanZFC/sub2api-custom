<template>
  <dl class="grid grid-cols-1 gap-4 text-sm sm:grid-cols-2">
    <div><dt class="text-gray-500 dark:text-gray-400">{{ t('invoice.requestId') }}</dt><dd class="mt-1 font-mono">#{{ request.id }}</dd></div>
    <div><dt class="text-gray-500 dark:text-gray-400">{{ t('invoice.status') }}</dt><dd class="mt-1"><InvoiceStatusBadge :status="request.status" /></dd></div>
    <div v-if="showUser" class="sm:col-span-2"><dt class="text-gray-500 dark:text-gray-400">{{ t('invoice.applicant') }}</dt><dd class="mt-1 break-all">{{ request.user_email }} (#{{ request.user_id }})</dd></div>
    <div><dt class="text-gray-500 dark:text-gray-400">{{ t('invoice.amount') }}</dt><dd class="mt-1 text-lg font-semibold text-primary-600 dark:text-primary-400">¥{{ request.amount.toFixed(2) }}</dd></div>
    <div><dt class="text-gray-500 dark:text-gray-400">{{ t('invoice.feeAmount') }}</dt><dd class="mt-1" data-testid="invoice-detail-fee">{{ request.fee_amount.toFixed(2) }}<span v-if="request.fee_refunded" class="ml-2 text-green-600 dark:text-green-400">{{ t('invoice.feeRefunded') }}</span></dd></div>
    <div><dt class="text-gray-500 dark:text-gray-400">{{ t('invoice.buyerType') }}</dt><dd class="mt-1">{{ t(`invoice.${request.buyer_type}`) }}</dd></div>
    <div><dt class="text-gray-500 dark:text-gray-400">{{ t('invoice.invoiceTitle') }}</dt><dd class="mt-1 break-all">{{ request.title }}</dd></div>
    <div><dt class="text-gray-500 dark:text-gray-400">{{ t('invoice.taxId') }}</dt><dd class="mt-1 break-all">{{ request.tax_id || '—' }}</dd></div>
    <div class="sm:col-span-2"><dt class="text-gray-500 dark:text-gray-400">{{ t('invoice.emails') }}</dt><dd class="mt-1 break-all">{{ request.emails.join(', ') }}</dd></div>
    <div class="sm:col-span-2"><dt class="text-gray-500 dark:text-gray-400">{{ t('invoice.orders') }}</dt><dd class="mt-1 max-h-32 overflow-y-auto break-words font-mono">{{ request.order_ids.map(id => `#${id}`).join(', ') }}</dd></div>
    <div><dt class="text-gray-500 dark:text-gray-400">{{ t('invoice.createdAt') }}</dt><dd class="mt-1">{{ formatOrderDateTime(request.created_at) }}</dd></div>
    <div v-if="request.issued_at"><dt class="text-gray-500 dark:text-gray-400">{{ t('invoice.issuedAt') }}</dt><dd class="mt-1">{{ formatOrderDateTime(request.issued_at) }}</dd></div>
    <div v-if="request.invoice_number" class="sm:col-span-2"><dt class="text-gray-500 dark:text-gray-400">{{ t('invoice.invoiceNumber') }}</dt><dd class="mt-1 break-all font-mono">{{ request.invoice_number }}</dd></div>
    <div v-if="request.remark" class="sm:col-span-2"><dt class="text-gray-500 dark:text-gray-400">{{ t('invoice.remark') }}</dt><dd class="mt-1 whitespace-pre-wrap break-words">{{ request.remark }}</dd></div>
    <div v-if="request.admin_remark" class="sm:col-span-2"><dt class="text-gray-500 dark:text-gray-400">{{ t('invoice.adminRemark') }}</dt><dd class="mt-1 whitespace-pre-wrap break-words">{{ request.admin_remark }}</dd></div>
  </dl>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { InvoiceRequest } from '@/types/invoice'
import { formatOrderDateTime } from '@/components/payment/orderUtils'
import InvoiceStatusBadge from './InvoiceStatusBadge.vue'

defineProps<{ request: InvoiceRequest; showUser?: boolean }>()
const { t } = useI18n()
</script>
