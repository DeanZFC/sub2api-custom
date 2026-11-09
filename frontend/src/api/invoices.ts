import { apiClient } from './client'
import type { BasePaginationResponse } from '@/types'
import type { CreateInvoiceRequest, InvoiceConfig, InvoiceOrdersResponse, InvoiceRequest, InvoiceStatus } from '@/types/invoice'

interface PaginationParams {
  page?: number
  page_size?: number
}

export const invoiceAPI = {
  getConfig() {
    return apiClient.get<InvoiceConfig>('/payment/invoices/config')
  },
  getOrders(params?: PaginationParams) {
    return apiClient.get<InvoiceOrdersResponse>('/payment/invoices/orders', { params })
  },
  getRequests(params?: PaginationParams) {
    return apiClient.get<BasePaginationResponse<InvoiceRequest>>('/payment/invoices', { params })
  },
  createRequest(data: CreateInvoiceRequest) {
    return apiClient.post<InvoiceRequest>('/payment/invoices', data)
  },
  downloadPDF(id: number) {
    return apiClient.get<Blob>(`/payment/invoices/${id}/pdf`, { responseType: 'blob' })
  },
}

export const adminInvoiceAPI = {
  getConfig() {
    return apiClient.get<InvoiceConfig>('/admin/payment/invoices/config')
  },
  updateConfig(data: Pick<InvoiceConfig, 'enabled' | 'min_amount' | 'instructions'>) {
    return apiClient.put<InvoiceConfig>('/admin/payment/invoices/config', data)
  },
  getRequests(params?: PaginationParams & { status?: InvoiceStatus }) {
    return apiClient.get<BasePaginationResponse<InvoiceRequest>>('/admin/payment/invoices', { params })
  },
  reject(id: number, data: { admin_remark: string }) {
    return apiClient.post<InvoiceRequest>(`/admin/payment/invoices/${id}/reject`, data)
  },
  issue(id: number, data: FormData) {
    return apiClient.post<InvoiceRequest>(`/admin/payment/invoices/${id}/issue`, data, {
      headers: { 'Content-Type': 'multipart/form-data' },
    })
  },
  downloadPDF(id: number) {
    return apiClient.get<Blob>(`/admin/payment/invoices/${id}/pdf`, { responseType: 'blob' })
  },
}

/** Download through the authenticated API; invoice files never use public URLs. */
export function saveInvoicePDF(data: Blob, request: InvoiceRequest) {
  const url = URL.createObjectURL(data)
  const link = document.createElement('a')
  link.href = url
  link.download = request.file_name || `invoice-${request.id}.pdf`
  link.click()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}
