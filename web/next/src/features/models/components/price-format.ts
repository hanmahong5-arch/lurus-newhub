import { useMoneyConfig } from '@/lib/status';

import { formatPrice } from '../lib/catalog';

/** Price formatter bound to the operator's display currency (lib/money.ts). */
export function usePriceFormatter() {
  const money = useMoneyConfig();
  return (usd: number | null | undefined) => formatPrice(usd, money);
}
