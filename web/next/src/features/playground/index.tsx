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
import { useQuery } from '@tanstack/react-query'
import { useCallback, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { LoadingState } from '@/components/loading-state'
import { currentUserQueryOptions } from '@/lib/user'

import { PlaygroundChat } from './components/chat/playground-chat'
import { PlaygroundInput } from './components/input/playground-input'
import {
  useChatHandler,
  usePlaygroundConversation,
  usePlaygroundOptions,
  usePlaygroundState,
} from './hooks'
import { setPlaygroundStorageScope } from './lib'

function PlaygroundWorkspace() {
  const { t } = useTranslation()
  const {
    config,
    parameterEnabled,
    messages,
    isLoadingMessages,
    models,
    groups,
    updateMessages,
    setModels,
    setGroups,
    updateConfig,
    updateParameterEnabled,
    clearMessages,
  } = usePlaygroundState()

  const { sendChat, stopGeneration, isGenerating } = useChatHandler({
    config,
    parameterEnabled,
    onMessageUpdate: updateMessages,
  })

  const {
    editingMessageKey,
    handleSendMessage,
    handleRegenerateMessage,
    handleEditMessage,
    handleEditOpenChange,
    applyEdit,
    handleDeleteMessage,
  } = usePlaygroundConversation({
    messages,
    updateMessages,
    sendChat,
  })

  const { isLoadingModels } = usePlaygroundOptions({
    currentGroup: config.group,
    currentModel: config.model,
    setGroups,
    setModels,
    updateConfig,
  })

  const handleClearMessages = () => {
    handleEditOpenChange(false)
    clearMessages()
  }

  // The model comes from the user's own list; with none selected there is
  // nothing meaningful to send.
  const submitPrompt = useCallback(
    (text: string) => {
      if (!config.model) {
        toast.error(t('Select a model before sending a message.'))
        return
      }
      handleSendMessage(text)
    },
    [config.model, handleSendMessage, t]
  )

  return (
    <div className='relative flex h-[calc(100svh-4.5rem)] min-h-[28rem] w-full min-w-0 flex-1 flex-col overflow-hidden sm:h-[calc(100svh-5rem)]'>
      {/* Full-width scroll container: scrolling works even over side whitespace */}
      <div className='flex min-h-0 flex-1 flex-col overflow-hidden'>
        <PlaygroundChat
          messages={messages}
          isLoadingMessages={isLoadingMessages}
          onRegenerateMessage={handleRegenerateMessage}
          onEditMessage={handleEditMessage}
          onDeleteMessage={handleDeleteMessage}
          onSelectPrompt={submitPrompt}
          isGenerating={isGenerating}
          editingKey={editingMessageKey}
          onCancelEdit={handleEditOpenChange}
          onSaveEdit={(newContent) => applyEdit(newContent, false)}
          onSaveEditAndSubmit={(newContent) => applyEdit(newContent, true)}
        />
      </div>

      {/* Input area: center content and constrain to the same container width */}
      <div className='mx-auto w-full max-w-4xl'>
        <PlaygroundInput
          config={config}
          disabled={isGenerating}
          groups={groups}
          groupValue={config.group}
          isGenerating={isGenerating}
          isModelLoading={isLoadingModels}
          modelValue={config.model}
          models={models}
          onGroupChange={(value) => updateConfig('group', value)}
          onConfigChange={updateConfig}
          onClearMessages={handleClearMessages}
          onModelChange={(value) => updateConfig('model', value)}
          onParameterEnabledChange={updateParameterEnabled}
          onStop={stopGeneration}
          onSubmit={submitPrompt}
          parameterEnabled={parameterEnabled}
          hasMessages={messages.length > 0}
          messages={messages}
        />
      </div>
    </div>
  )
}

/**
 * Conversations live in this browser only, and the browser is shared between
 * accounts, so storage is scoped to the signed-in user before the workspace
 * reads it for the first time.
 */
export function PlaygroundPage() {
  const { t } = useTranslation()
  const { data: user } = useQuery(currentUserQueryOptions)
  const [scopedUserId, setScopedUserId] = useState<number | null>(null)

  if (!user) {
    return <LoadingState className='py-24' message={t('Loading...')} />
  }
  if (scopedUserId !== user.id) {
    // Runs before the workspace mounts, so its first storage read is scoped.
    setPlaygroundStorageScope(String(user.id))
    setScopedUserId(user.id)
    return null
  }
  return <PlaygroundWorkspace key={user.id} />
}
