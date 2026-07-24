export interface HealthResponse {
  status: 'healthy';
  service: string;
  timestamp: string;
}

export async function fetchHealth(url: string): Promise<HealthResponse> {
  const response = await fetch(url);
  if (!response.ok) {
    throw new Error(
      `Health check failed with status ${String(response.status)}`,
    );
  }

  const data = (await response.json()) as Partial<HealthResponse>;
  if (
    data.status !== 'healthy' ||
    typeof data.service !== 'string' ||
    typeof data.timestamp !== 'string'
  ) {
    throw new Error('Health response was malformed');
  }

  return data as HealthResponse;
}
