import { useEffect, useState, type RefObject } from 'react'
import { toast } from '../../../shared/components'
import { fetchBrowserProfiles } from '../api'
import type { BrowserProfile } from '../types'
import { sortProfiles } from './QuickLaunchModal.helpers'

export function useQuickLaunchData(open: boolean, inputRef: RefObject<HTMLInputElement | null>) {
  const [profiles, setProfiles] = useState<BrowserProfile[]>([])
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    if (!open) return

    let alive = true
    setLoading(true)

    fetchBrowserProfiles()
      .then((profileItems) => {
        if (!alive) return
        setProfiles((profileItems || []).slice().sort(sortProfiles))
      })
      .catch(() => {
        if (!alive) return
        toast.error('加载实例列表失败')
        setProfiles([])
      })
      .finally(() => {
        if (!alive) return
        setLoading(false)
        setTimeout(() => inputRef.current?.focus(), 0)
      })

    return () => {
      alive = false
    }
  }, [inputRef, open])

  return { profiles, loading }
}
