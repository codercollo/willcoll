import { defineStore } from 'pinia'
import type { PlotMeterReading, Report, ReportChecks } from '~/types/report'

// The schedule and receipts are PDFs rendered on demand (nothing is stored), so
// they always reflect the ledger as it is now.
export const useReports = defineStore('reports', () => {
  const api = useApi()

  async function get(propertyId: string, period: string) {
    return (await api.get<{ report: Report }>(`/properties/${propertyId}/reports/${period}`)).report
  }

  /** The sanity panel + NOTE:1/NOTE:2 preview, and the confirm/stale status. */
  async function checks(propertyId: string, period: string) {
    return await api.get<ReportChecks>(`/properties/${propertyId}/reports/${period}/checks`)
  }

  async function confirm(propertyId: string, period: string) {
    return (await api.post<{ report: Report }>(`/properties/${propertyId}/reports/${period}/confirm`)).report
  }

  /** The plot meter reading form's state: previous, current (if saved), units used. */
  async function plotMeter(propertyId: string, period: string) {
    return (await api.get<{ plot_meter: PlotMeterReading }>(`/properties/${propertyId}/reports/${period}/plot-meter`)).plot_meter
  }

  /** currentReading is sent as a decimal string, matching every other money field. previousOverride only needs sending for the first-ever reading (nothing to default from). */
  async function setPlotMeter(propertyId: string, period: string, currentReading: string, previousOverride?: string) {
    await api.put(`/properties/${propertyId}/reports/${period}/plot-meter`, { current_reading: currentReading, previous_reading: previousOverride })
  }

  function openSchedule(propertyId: string, period: string) {
    return api.openPdf(`/properties/${propertyId}/reports/${period}/generate`, { method: 'POST' })
  }

  function openReceipts(propertyId: string, period: string, unitId?: string) {
    return api.openPdf(`/properties/${propertyId}/receipts/${period}/download`, { method: 'GET', query: { unit_id: unitId } })
  }

  return { get, checks, confirm, plotMeter, setPlotMeter, openSchedule, openReceipts }
})
