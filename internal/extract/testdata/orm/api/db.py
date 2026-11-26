class Produto(Base):
    __tablename__ = "produtos"

def buscar(session):
    return session.query(Produto).all()
