ifneq (,$(wildcard ./.env))
    include .env
    export
endif

migration:
	-su -c 'createdb ichihime' $(DBUSER)
	su -c 'psql -d ichihime -f ./scripts/dbset.sql' $(DBUSER)
