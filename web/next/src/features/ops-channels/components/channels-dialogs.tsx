/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { useChannels } from './channels-provider'
import { ChannelOpsSheet } from './channel-drawer'
import { BalanceQueryDialog } from './dialogs/balance-query-dialog'
import { ChannelTestDialog } from './dialogs/channel-test-dialog'
import { CopyChannelDialog } from './dialogs/copy-channel-dialog'
import { EditTagDialog } from './dialogs/edit-tag-dialog'
import { FetchModelsDialog } from './dialogs/fetch-models-dialog'
import { MultiKeyManageDialog } from './dialogs/multi-key-manage-dialog'
import { OllamaModelsDialog } from './dialogs/ollama-models-dialog'
import { TagBatchEditDialog } from './dialogs/tag-batch-edit-dialog'
import { ChannelMutateDrawer } from './drawers/channel-mutate-drawer'

/** Every dialog and drawer the channel table can open, keyed off the context. */
export function ChannelsDialogs() {
  const { open, setOpen, currentRow } = useChannels()
  const close = (isOpen: boolean) => {
    if (!isOpen) setOpen(null)
  }

  return (
    <>
      <ChannelMutateDrawer
        open={open === 'create-channel' || open === 'update-channel'}
        onOpenChange={close}
        currentRow={open === 'update-channel' ? currentRow : null}
      />
      <ChannelTestDialog open={open === 'test-channel'} onOpenChange={close} />
      <BalanceQueryDialog
        open={open === 'balance-query'}
        onOpenChange={close}
      />
      <FetchModelsDialog open={open === 'fetch-models'} onOpenChange={close} />
      <OllamaModelsDialog
        open={open === 'ollama-models'}
        onOpenChange={close}
      />
      <CopyChannelDialog open={open === 'copy-channel'} onOpenChange={close} />
      <MultiKeyManageDialog
        open={open === 'multi-key-manage'}
        onOpenChange={close}
      />
      <TagBatchEditDialog
        open={open === 'tag-batch-edit'}
        onOpenChange={close}
      />
      <EditTagDialog open={open === 'edit-tag'} onOpenChange={close} />
      <ChannelOpsSheet open={open === 'channel-ops'} onOpenChange={close} />
    </>
  )
}
