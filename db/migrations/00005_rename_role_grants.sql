-- +goose Up
ALTER TABLE user_roles RENAME TO role_grants;
ALTER INDEX user_roles_pkey RENAME TO role_grants_pkey;
ALTER TABLE role_grants RENAME CONSTRAINT user_roles_user_id_fkey TO role_grants_user_id_fkey;

-- +goose Down
ALTER TABLE role_grants RENAME CONSTRAINT role_grants_user_id_fkey TO user_roles_user_id_fkey;
ALTER INDEX role_grants_pkey RENAME TO user_roles_pkey;
ALTER TABLE role_grants RENAME TO user_roles;
