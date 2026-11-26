from django.db import models

class Base(models.Model):
    class Meta:
        abstract = True

class Pedido(models.Model):
    total = models.DecimalField()

class Cliente(models.Model):
    class Meta:
        db_table = "clientes"
