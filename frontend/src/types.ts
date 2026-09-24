export type Intent = 'refund' | 'order' | 'tech' | 'other' | 'unknown';

export interface ChatMessage {
  id: string;
  role: 'user' | 'assistant' | 'agent' | 'system';
  content: string;
  model?: string;
  intent?: Intent;
  intentConfidence?: number;
  createdAt: string;
}

export interface ChatResponse {
  conversationId: string;
  message: ChatMessage;
  userMessage: ChatMessage;
  intent: Intent;
  intentConfidence: number;
  handedOver: boolean;
  source: 'faq' | 'llm' | 'handover' | string;
}

export interface ChatRequest {
  conversationId?: string;
  userId?: string;
  content: string;
}

export interface FeedbackInput {
  conversationId: string;
  rating: number;
  comment?: string;
}

export interface Conversation {
  id: string;
  userId: string;
  title: string;
  status: 'open' | 'handed_over' | 'closed';
  handedOver: boolean;
  createdAt: string;
  updatedAt: string;
}

export interface ConversationDetail {
  conversation: Conversation;
  messages: ChatMessage[];
}

export interface ListConversationsResponse {
  total: number;
  items: Conversation[];
  page: number;
  pageSize: number;
}

export interface SatisfactionStat {
  total: number;
  average: number;
  distribution: [number, number, number, number, number];
  byDay: { day: string; count: number; avg: number }[];
}

export interface LoginResponse {
  token: string;
  expiresIn: number;
  username: string;
  role: string;
}