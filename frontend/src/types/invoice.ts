import type { BasePaginationResponse } from '@/types'

export interface InvoiceConfig {
  enabled: boolean
  min_amount: number
  instructions: string
  currency: 'CNY'
}

export interface InvoiceOrder {
  id: number
  pay_amount: number
  currency: string
  completed_at: string | null
}

export interface InvoiceOrdersResponse extends BasePaginationResponse<InvoiceOrder> {
  available_amount: number
}

export type InvoiceStatus = 'pending' | 'issued' | 'rejected'

export interface InvoiceRequest {
  id: number
  user_id: number
  user_email: string
  order_ids: number[]
  amount: number
  currency: string
  buyer_type: 'personal' | 'company'
  title: string
  tax_id: string
  emails: string[]
  remark: string
  status: InvoiceStatus
  admin_remark: string
  invoice_number: string
  file_name: string
  created_at: string
  updated_at: string
  issued_at?: string
}

export interface CreateInvoiceRequest {
  order_ids: number[]
  buyer_type: 'personal' | 'company'
  title: string
  tax_id: string
  emails: string[]
  remark: string
}
