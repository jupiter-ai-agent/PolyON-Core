-- Migration 003: AI Agent + Memory 컴포넌트 상태 업데이트
-- Phase 7 (AI Agent + Memory) 배포에 따라 openclaw, mem0 상태를 planned → deployed로 변경.

UPDATE polyon_components
   SET status     = 'deployed',
       updated_at = NOW()
 WHERE id IN ('openclaw', 'mem0')
   AND status = 'planned';

-- mem0의 health_endpoint 업데이트 (server.py /health 반환 확인)
UPDATE polyon_components
   SET health_endpoint = '/health',
       updated_at      = NOW()
 WHERE id = 'mem0';
