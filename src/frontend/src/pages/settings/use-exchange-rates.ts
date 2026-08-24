import { useEffect, useState } from 'react'

import { api, errorMessage } from '@/lib/api'
import { CURRENCIES } from '@/lib/format'
import type { ExchangeRateSettings } from '@/lib/types'

/**
 * 费用换算卡片的状态与汇率操作。
 * reportingCurrency 属于主设置表单（随统一保存提交），由父级传入。
 */
export function useExchangeRates({
  reportingCurrency,
  savedReportingCurrency,
  onError,
}: {
  reportingCurrency: string
  savedReportingCurrency: string
  onError: (message: string) => void
}) {
  const [exchangeData, setExchangeData] = useState<ExchangeRateSettings | null>(null)
  const [customSource, setCustomSource] = useState('')
  const [customSourceAmount, setCustomSourceAmount] = useState('1')
  const [customTargetAmount, setCustomTargetAmount] = useState('')
  const [customBaseSide, setCustomBaseSide] = useState<'source' | 'target'>('source')
  const [refreshingRates, setRefreshingRates] = useState(false)
  const [publicRatesOpen, setPublicRatesOpen] = useState(false)
  const [loadingPublicRates, setLoadingPublicRates] = useState(false)
  const [publicRatesError, setPublicRatesError] = useState('')
  const [deletingCustomRateID, setDeletingCustomRateID] = useState<number | null>(null)

  useEffect(() => {
    api
      .exchangeRates()
      .then(setExchangeData)
      .catch(() => {})
  }, [])

  const reload = async () => {
    setExchangeData(await api.exchangeRates())
  }

  const refreshRates = async () => {
    setRefreshingRates(true)
    try {
      setExchangeData(await api.refreshExchangeRates())
    } catch (err) {
      onError(errorMessage(err))
    } finally {
      setRefreshingRates(false)
    }
  }

  const showPublicRates = async () => {
    setPublicRatesOpen(true)
    setLoadingPublicRates(true)
    setPublicRatesError('')
    try {
      setExchangeData(await api.exchangeRates())
    } catch (err) {
      setPublicRatesError(errorMessage(err))
    } finally {
      setLoadingPublicRates(false)
    }
  }

  const addCustomRate = async () => {
    try {
      await api.saveCustomExchangeRate({
        id: 0,
        source_currency: customSource,
        source_amount: customSourceAmount,
        target_currency: reportingCurrency,
        target_amount: customTargetAmount,
        enabled: true,
      })
      setExchangeData(await api.exchangeRates())
      setCustomSource('')
      setCustomSourceAmount(customBaseSide === 'source' ? '1' : '')
      setCustomTargetAmount('')
    } catch (err) {
      onError(errorMessage(err))
    }
  }

  const changeCustomBaseSide = (side: 'source' | 'target') => {
    setCustomBaseSide(side)
    setCustomSourceAmount(side === 'source' ? '1' : '')
    setCustomTargetAmount(side === 'target' ? '1' : '')
  }

  const setCustomRateEnabled = async (
    rate: ExchangeRateSettings['custom_rates'][number],
    enabled: boolean,
  ) => {
    try {
      await api.saveCustomExchangeRate({ ...rate, enabled })
      setExchangeData(await api.exchangeRates())
    } catch (err) {
      onError(errorMessage(err))
    }
  }

  const deleteCustomRate = async (id: number) => {
    setDeletingCustomRateID(id)
    onError('')
    try {
      await api.deleteCustomExchangeRate(id)
      setExchangeData(await api.exchangeRates())
    } catch (err) {
      onError(errorMessage(err))
    } finally {
      setDeletingCustomRateID(null)
    }
  }

  const configuredSources = new Set(
    (exchangeData?.custom_rates ?? []).map((rate) => rate.source_currency),
  )
  const customSourceOptions = CURRENCIES.filter(
    (currency) => currency !== reportingCurrency && !configuredSources.has(currency),
  )
  const reportingCurrencyPending = reportingCurrency !== savedReportingCurrency
  const customRateReady = Boolean(
    customSource &&
    customSourceAmount &&
    customTargetAmount &&
    (Number(customSourceAmount) === 1 || Number(customTargetAmount) === 1) &&
    !reportingCurrencyPending,
  )

  return {
    exchangeData,
    customSource,
    customSourceAmount,
    customTargetAmount,
    customBaseSide,
    refreshingRates,
    publicRatesOpen,
    loadingPublicRates,
    publicRatesError,
    deletingCustomRateID,
    customSourceOptions,
    reportingCurrencyPending,
    customRateReady,
    setPublicRatesOpen,
    setCustomSource,
    setCustomSourceAmount,
    setCustomTargetAmount,
    reload,
    refreshRates,
    showPublicRates,
    addCustomRate,
    changeCustomBaseSide,
    setCustomRateEnabled,
    deleteCustomRate,
  }
}

export type ExchangeRatesController = ReturnType<typeof useExchangeRates>
