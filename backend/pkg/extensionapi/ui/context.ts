import type { ComputedRef, InjectionKey } from 'vue'

export const extensionAvailabilityKey: InjectionKey<ComputedRef<boolean>> = Symbol('extensionAvailability')
export const extensionUnavailableMessageKey: InjectionKey<ComputedRef<string>> = Symbol('extensionUnavailableMessage')
