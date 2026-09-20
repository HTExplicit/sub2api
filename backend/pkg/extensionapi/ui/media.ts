export type ImageStudioMode = 'generate' | 'edit'
export type ImageStudioJobStatus = 'pending' | 'preparing' | 'running' | 'succeeded' | 'partially_succeeded' | 'failed' | 'canceled' | 'canceled_with_results'
export interface ImageStudioHistoryImage { id: string; blob: Blob; mimeType: string; revisedPrompt?: string }
export interface ImageStudioHistoryRecord {
  id: string; jobId?: number; status?: ImageStudioJobStatus; errorMessage?: string; createdAt: number
  mode: ImageStudioMode; model: string; prompt: string; size: string; quality: string; count: number
  sourceImage?: Blob; sourceImageName?: string; maskImage?: Blob; maskImageName?: string
  images: ImageStudioHistoryImage[]
}
