import { useTranslation } from 'react-i18next'

import { Card, CardContent } from '@/components/ui/card'
import type { MoneyConfig } from '@/lib/money'

import type { LogStat } from '../lib/logs'
import { statCells } from '../lib/stat-cells'

interface StatHeaderProps {
  stat: LogStat | undefined
  loading: boolean
  /** The stat read failed: cells show "--" and say so, never a made-up zero. */
  failed: boolean
  money: MoneyConfig | null
}

export function StatHeader(props: StatHeaderProps) {
  const { t } = useTranslation()
  const cells = statCells(props.stat, props.money)
  return (
    <div data-testid='log-stat-header' className='space-y-1'>
      <div className='grid grid-cols-2 gap-3 md:grid-cols-3 xl:grid-cols-6'>
        {cells.map((c) => (
          <Card key={c.id} size='sm' data-testid={`log-stat-${c.id}`}>
            <CardContent>
              <div className='text-muted-foreground text-xs'>
                {t(c.label)}
                <span className='ml-1 opacity-70'>· {t(c.hint)}</span>
              </div>
              <div className='mt-1 text-xl font-semibold tabular-nums'>
                {props.loading && !props.stat ? '…' : c.value}
              </div>
            </CardContent>
          </Card>
        ))}
      </div>
      {props.failed && (
        <p role='alert' className='text-destructive text-xs'>
          {t('Failed to load statistics')}
        </p>
      )}
    </div>
  )
}
