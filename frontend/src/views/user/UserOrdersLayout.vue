<template>
  <AppLayout>
    <nav class="tabs mb-4 w-fit" :aria-label="t('nav.myOrders')">
      <RouterLink :to="{ name: 'OrderList' }" class="tab" exact-active-class="tab-active">
        {{ t('payment.orders.list') }}
      </RouterLink>
      <RouterLink :to="{ name: 'UserInvoices' }" class="tab" exact-active-class="tab-active">
        {{ t('invoice.title') }}
      </RouterLink>
    </nav>
    <RouterView />
  </AppLayout>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'

const { t } = useI18n()
</script>
