from .models import Pedido, Cliente

def listar(request):
    Pedido.objects.filter(pago=True).update(enviado=True)
    return Cliente.objects.all()
